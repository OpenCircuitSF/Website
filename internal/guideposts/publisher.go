package guideposts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

const (
	defaultPollInterval = 15 * time.Second
	// sendBudget bounds one post end to end.
	sendBudget = 60 * time.Second
	// writeBudget bounds the status writes after a send, independent of the
	// send context.
	writeBudget = 10 * time.Second
	// orphanStaleAfter is how long a claimed row may sit in sending before
	// the sweep returns it to the queue.
	orphanStaleAfter = 5 * time.Minute
	promoteBatch     = 10
	claimBatch       = 5

	settingQueueMaxRetries = "queue_max_retries"
)

// publisherKinds scopes every claim, sweep and select to guide posts, so
// this publisher never touches an email or an Outreach post.
var publisherKinds = []outbox.Kind{outbox.KindGuidePost}

// PublisherDeps are the Publisher's collaborators.
type PublisherDeps struct {
	Store    *Store
	Outbox   *outbox.Store
	Settings outreach.Settings
	// Clients yields the current Bluesky and Mastodon clients, the same
	// accounts Outreach posts as.
	Clients outreach.ClientSource
	// CardFn returns a guide's Open Graph card for the Bluesky thumbnail
	// (seo.Site.GuideCard). Nil, or an error, posts without one.
	CardFn       func(slug string) ([]byte, string, error)
	PollInterval time.Duration // default 15s
	Log          *slog.Logger
}

// Publisher promotes due guide posts onto the outbound queue and posts
// claimed rows. Run it in one goroutine; Stop it.
type Publisher struct {
	store    *Store
	outbox   *outbox.Store
	settings outreach.Settings
	clients  outreach.ClientSource
	cardFn   func(slug string) ([]byte, string, error)
	poll     time.Duration
	log      *slog.Logger

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	runMu    sync.Mutex
	running  bool
}

// NewPublisher builds a Publisher. It starts nothing.
func NewPublisher(d PublisherDeps) *Publisher {
	if d.PollInterval <= 0 {
		d.PollInterval = defaultPollInterval
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Publisher{
		store: d.Store, outbox: d.Outbox, settings: d.Settings, clients: d.Clients,
		cardFn: d.CardFn, poll: d.PollInterval, log: d.Log,
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
}

func (p *Publisher) bluesky() *bluesky.Client {
	if p.clients == nil {
		return nil
	}
	return p.clients.Bluesky()
}

func (p *Publisher) mastodon() *mastodon.Client {
	if p.clients == nil {
		return nil
	}
	return p.clients.Mastodon()
}

// ChannelConfigured reports whether the named channel has a working client.
// Safe on a nil receiver.
func (p *Publisher) ChannelConfigured(name string) bool {
	if p == nil {
		return false
	}
	return channelEnabled(name, p.bluesky(), p.mastodon())
}

func channelEnabled(name string, bs *bluesky.Client, ma *mastodon.Client) bool {
	switch name {
	case "bluesky":
		return bs.Enabled()
	case "mastodon":
		return ma.Enabled()
	}
	return false
}

// Run blocks, passing every poll interval, until Stop is called or ctx is
// done.
func (p *Publisher) Run(ctx context.Context) {
	p.runMu.Lock()
	p.running = true
	p.runMu.Unlock()
	defer close(p.doneCh)
	for {
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}
		worked, err := p.pass(ctx)
		if err != nil {
			p.log.Error("guideposts: publisher pass failed", "err", err)
		}
		if worked {
			continue
		}
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(p.poll):
		}
	}
}

// Stop tells Run to finish its current row and return, and waits for it or
// for ctx. A row mid-send when ctx expires is returned to the queue by the
// next orphan sweep. Safe to call more than once, and when Run never started.
func (p *Publisher) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.runMu.Lock()
	running := p.running
	p.runMu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-p.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pass is one sweep, promote, claim and send cycle.
func (p *Publisher) pass(ctx context.Context) (worked bool, err error) {
	if _, err := p.outbox.OrphanSweep(ctx, orphanStaleAfter, publisherKinds); err != nil {
		return false, fmt.Errorf("orphan sweep: %w", err)
	}
	promoted, held, err := p.store.PromoteDue(ctx, promoteBatch)
	if err != nil {
		return false, fmt.Errorf("promote due: %w", err)
	}
	if held > 0 {
		p.log.Warn("guideposts: scheduled posts held because their guide no longer exists", "held", held)
	}
	if promoted > 0 {
		p.log.Info("guideposts: scheduled posts queued", "count", promoted)
	}
	ids, err := p.outbox.SelectDue(ctx, claimBatch, publisherKinds)
	if err != nil {
		return false, fmt.Errorf("select due: %w", err)
	}
	for _, id := range ids {
		select {
		case <-p.stopCh:
			return worked, nil
		case <-ctx.Done():
			return worked, nil
		default:
		}
		row, claimed, err := p.outbox.ClaimRow(ctx, id)
		if err != nil {
			p.log.Warn("guideposts: claim failed", "id", id, "err", err)
			continue
		}
		if !claimed {
			continue
		}
		worked = true
		p.sendOne(ctx, row)
	}
	return worked, nil
}

type postPayload struct {
	PostID int64 `json:"post_id"`
}

func (p *Publisher) sendOne(parent context.Context, row outbox.Row) {
	ctx, cancel := context.WithTimeout(parent, sendBudget)
	defer cancel()

	var pl postPayload
	if err := json.Unmarshal(row.Payload, &pl); err != nil || pl.PostID <= 0 {
		p.log.Warn("guideposts: undecodable payload, abandoning row", "id", row.ID)
		p.settle(row, "bad payload", 0)
		return
	}
	job, err := p.store.LoadForSend(ctx, pl.PostID)
	if errors.Is(err, ErrNotFound) {
		p.markDone(row)
		return
	}
	if err != nil {
		p.log.Warn("guideposts: could not load post", "id", row.ID, "post_id", pl.PostID, "err", err)
		p.settle(row, "could not load the post; retrying", p.maxRetries())
		return
	}
	if job.Status != "queued" {
		p.markDone(row)
		return
	}
	// Captured once, so a Settings save mid-send cannot hand Post a nil client.
	bs, ma := p.bluesky(), p.mastodon()
	ch, ok := outreach.ChannelByName(job.Channel)
	if !ok || !channelEnabled(ch.Name, bs, ma) {
		name := ch.Label
		if name == "" {
			name = job.Channel
		}
		p.fail(row, job, name+" is not configured on this server", true)
		return
	}
	if msg := sendRefusal(ch, job); msg != "" {
		p.fail(row, job, msg, true)
		return
	}
	g, ok := topic(job.GuideSlug)
	if !ok {
		p.fail(row, job, HeldUnknownGuideMessage, true)
		return
	}

	var remoteID, remoteURL string
	switch ch.Name {
	case "bluesky":
		res, err := p.postBluesky(ctx, bs, job, g.Title, g.Hook)
		if err != nil {
			p.postFailed(row, job, ch, err, bluesky.IsPermanent(err))
			return
		}
		remoteID, remoteURL = res.URI, res.WebURL
		if res.ThumbErr != nil {
			p.log.Warn("guideposts: thumbnail upload failed, posted without it", "post_id", job.ID, "err", res.ThumbErr)
		}
	case "mastodon":
		st, adopted, err := p.postMastodon(ctx, ma, row, job)
		if err != nil {
			p.postFailed(row, job, ch, err, mastodon.IsPermanent(err))
			return
		}
		remoteID, remoteURL = st.ID, st.URL
		if adopted {
			p.log.Info("guideposts: adopted an existing Mastodon status instead of posting again", "post_id", job.ID)
		}
	}

	wctx, wcancel := context.WithTimeout(context.WithoutCancel(parent), writeBudget)
	defer wcancel()
	ok, err = p.store.MarkRemotePosted(wctx, job.ID, remoteID, remoteURL, time.Now())
	switch {
	case err != nil:
		p.log.Error("guideposts: posted but could not record it", "post_id", job.ID, "err", err)
		p.settle(row, "posted, but recording it failed; retrying", p.maxRetries())
		return
	case !ok:
		p.log.Warn("guideposts: posted but the post was no longer queued", "post_id", job.ID)
	}
	p.markDone(row)
}

// sendRefusal is why job cannot be posted to ch at all, or "". A queued post
// without a short link is only reachable by a manual SQL edit
// (guide_posts_link_check forbids it); it fails permanently, as Outreach's
// does.
func sendRefusal(ch outreach.Channel, job Post) string {
	if job.ShortURL == nil {
		return "Cannot post: the post has no short link"
	}
	if err := outreach.ValidateForSend(ch, job.Body); err != nil {
		return "Cannot post: " + err.Error()
	}
	return ""
}

// postMastodon posts the status, or, after an attempt of unknown outcome,
// first adopts a recent status carrying this post's short URL. Each post's
// short URL is unique, so an earlier post of the same guide never matches.
func (p *Publisher) postMastodon(ctx context.Context, masto *mastodon.Client, row outbox.Row, job Post) (mastodon.Status, bool, error) {
	prior := row.Attempts > 1
	if !prior {
		var err error
		if prior, err = p.outbox.HasPriorAttempt(ctx, row.Kind, row.Recipient, row.ID); err != nil {
			return mastodon.Status{}, false, err
		}
	}
	if prior {
		found, ok, err := masto.FindRecentStatusWithURL(ctx, *job.ShortURL)
		if err != nil {
			return mastodon.Status{}, false, err
		}
		if ok {
			return found, true, nil
		}
	}
	st, err := masto.PostStatus(ctx, job.Body, MastodonIdempotencyKey(job.ID, job.AttemptGeneration))
	return st, false, err
}

func (p *Publisher) postBluesky(ctx context.Context, bsky *bluesky.Client, job Post, title, hook string) (bluesky.PostResult, error) {
	in := bluesky.PostInput{
		Text:      job.Body,
		RKey:      outreach.BlueskyRKey(job.CreatedAt, job.ID, job.AttemptGeneration),
		CreatedAt: time.Now(),
		Embed:     &bluesky.ExternalCard{URI: *job.ShortURL, Title: title, Description: hook},
	}
	if p.cardFn != nil {
		data, mime, err := p.cardFn(job.GuideSlug)
		switch {
		case err != nil:
			p.log.Warn("guideposts: no card for thumbnail, posting without it", "post_id", job.ID, "err", err)
		case len(data) > bluesky.MaxThumbBytes:
			p.log.Warn("guideposts: card too large for a thumbnail, posting without it", "post_id", job.ID, "bytes", len(data))
		default:
			in.Embed.Thumb, in.Embed.ThumbMIME = data, mime
		}
	}
	return bsky.Post(ctx, in)
}

func (p *Publisher) postFailed(row outbox.Row, job Post, ch outreach.Channel, err error, permanent bool) {
	p.log.Warn("guideposts: post attempt failed", "post_id", job.ID, "channel", ch.Name, "attempt", row.Attempts, "permanent", permanent, "err", err)
	if permanent {
		p.fail(row, job, ch.Label+" rejected the post: "+briefError(err), true)
		return
	}
	max := p.maxRetries()
	verb := "retrying"
	if row.Attempts >= max {
		verb = "giving up"
	}
	p.fail(row, job, fmt.Sprintf("%s is unreachable or failing (attempt %d of %d); %s: %s", ch.Label, row.Attempts, max, verb, briefError(err)), false)
}

// briefError bounds a client error's text, which each client already keeps
// secret-free.
func briefError(err error) string {
	s := err.Error()
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "..."
	}
	return s
}

// fail records msg on the post and settles the queue row: permanent goes
// straight to abandoned and failed; otherwise the standard backoff applies.
func (p *Publisher) fail(row outbox.Row, job Post, msg string, permanent bool) {
	max := 0
	if !permanent {
		max = p.maxRetries()
	}
	final := permanent || row.Attempts >= max
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	if _, err := p.outbox.MarkRetryOrAbandon(ctx, row.ID, row.Attempts, msg, max); err != nil {
		p.log.Error("guideposts: could not settle queue row", "id", row.ID, "err", err)
	}
	if _, err := p.store.MarkSendError(ctx, job.ID, msg, final); err != nil {
		p.log.Error("guideposts: could not record send error", "post_id", job.ID, "err", err)
	}
}

func (p *Publisher) settle(row outbox.Row, msg string, max int) {
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	if _, err := p.outbox.MarkRetryOrAbandon(ctx, row.ID, row.Attempts, msg, max); err != nil {
		p.log.Error("guideposts: could not settle queue row", "id", row.ID, "err", err)
	}
}

func (p *Publisher) markDone(row outbox.Row) {
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	if _, err := p.outbox.MarkDone(ctx, row.ID); err != nil {
		p.log.Error("guideposts: could not close queue row", "id", row.ID, "err", err)
	}
}

// maxRetries reads settings.queue_max_retries, falling back to the outbox
// default.
func (p *Publisher) maxRetries() int {
	if p.settings == nil {
		return outbox.DefaultMaxRetries
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	raw, err := p.settings.GetSetting(ctx, settingQueueMaxRetries)
	if err != nil {
		return outbox.DefaultMaxRetries
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || n <= 0 {
		return outbox.DefaultMaxRetries
	}
	return n
}
