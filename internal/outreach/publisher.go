package outreach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

const (
	defaultPollInterval = 15 * time.Second
	// sendBudget bounds one post end to end (login, existence check,
	// thumbnail upload, create).
	sendBudget = 60 * time.Second
	// writeBudget bounds the status writes after a send. It is independent of
	// the send context, which may be spent by then.
	writeBudget = 10 * time.Second
	// orphanStaleAfter is how long a claimed row may sit in sending before the
	// sweep returns it to the queue: comfortably above one send budget.
	orphanStaleAfter = 5 * time.Minute
	promoteBatch     = 10
	claimBatch       = 5

	settingQueueMaxRetries = "queue_max_retries"
)

// publisherKinds scopes every claim, sweep and select to this publisher's
// own kind, so it never touches an email row and the mail worker never
// touches a post.
var publisherKinds = []outbox.Kind{outbox.KindOutreachPost}

// Settings reads a settings row.
type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// PublisherDeps are the Publisher's collaborators. A nil Bluesky or Mastodon
// client means that channel is not configured.
type PublisherDeps struct {
	Store    *Store
	Outbox   *outbox.Store
	Settings Settings
	Bluesky  *bluesky.Client
	Mastodon *mastodon.Client
	// MediaDir is where /media/ cover images live on disk; empty disables
	// thumbnails.
	MediaDir     string
	PollInterval time.Duration // default 15s
	Log          *slog.Logger
}

// Publisher promotes due scheduled posts onto the outbound queue and posts
// claimed rows to Bluesky and Mastodon. Run it in one goroutine; Stop it.
type Publisher struct {
	store    *Store
	outbox   *outbox.Store
	settings Settings
	bsky     *bluesky.Client
	masto    *mastodon.Client
	mediaDir string
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
		store: d.Store, outbox: d.Outbox, settings: d.Settings, bsky: d.Bluesky, masto: d.Mastodon,
		mediaDir: d.MediaDir, poll: d.PollInterval, log: d.Log,
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
}

// ChannelConfigured reports whether the named automatic channel has a working
// client. Safe on a nil receiver; false for manual channels.
func (p *Publisher) ChannelConfigured(name string) bool {
	if p == nil {
		return false
	}
	switch name {
	case "bluesky":
		return p.bsky.Enabled()
	case "mastodon":
		return p.masto.Enabled()
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
			p.log.Error("outreach: publisher pass failed", "err", err)
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
// for ctx. It claims and releases nothing itself: a row mid-send when ctx
// expires is left sending and returned to the queue by the next orphan sweep.
// Safe to call more than once, and safe when Run was never started.
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

// pass is one sweep, promote, claim and send cycle. worked reports whether it
// claimed anything, so Run loops again without sleeping.
func (p *Publisher) pass(ctx context.Context) (worked bool, err error) {
	if _, err := p.outbox.OrphanSweep(ctx, orphanStaleAfter, publisherKinds); err != nil {
		return false, fmt.Errorf("orphan sweep: %w", err)
	}
	promoted, held, err := p.store.PromoteDue(ctx, promoteBatch)
	if err != nil {
		return false, fmt.Errorf("promote due: %w", err)
	}
	if held > 0 {
		p.log.Warn("outreach: scheduled posts held because their workshop is not published", "held", held)
	}
	if promoted > 0 {
		p.log.Info("outreach: scheduled posts queued", "count", promoted)
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
			p.log.Warn("outreach: claim failed", "id", id, "err", err)
			continue
		}
		if !claimed {
			p.log.Warn("outreach: due row was claimed elsewhere", "id", id)
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
		p.log.Warn("outreach: undecodable payload, abandoning row", "id", row.ID)
		p.settle(row, "bad payload", 0)
		return
	}
	job, err := p.store.LoadForSend(ctx, pl.PostID)
	if errors.Is(err, ErrNotFound) {
		p.log.Warn("outreach: post is gone, closing row", "id", row.ID, "post_id", pl.PostID)
		p.markDone(row)
		return
	}
	if err != nil {
		p.log.Warn("outreach: could not load post", "id", row.ID, "post_id", pl.PostID, "err", err)
		p.settle(row, "could not load the post; retrying", p.maxRetries())
		return
	}
	if job.Status != "queued" {
		p.log.Warn("outreach: post is no longer queued, closing row", "id", row.ID, "post_id", job.ID, "status", job.Status)
		p.markDone(row)
		return
	}
	if job.WorkshopStatus != "published" {
		p.hold(row, job)
		return
	}
	ch, ok := ChannelByName(job.Channel)
	if !ok || !p.ChannelConfigured(job.Channel) {
		p.fail(row, job, label(ch, job.Channel)+" is not configured on this server", true)
		return
	}
	if err := ValidateForSend(ch, job.Body); err != nil {
		p.fail(row, job, "Cannot post: "+err.Error(), true)
		return
	}
	if job.ShortURL == nil {
		p.fail(row, job, "Cannot post: the post has no short link", true)
		return
	}

	var remoteID, remoteURL string
	switch ch.Name {
	case "bluesky":
		res, err := p.postBluesky(ctx, job)
		if err != nil {
			p.postFailed(row, job, ch, err, bluesky.IsPermanent(err))
			return
		}
		remoteID, remoteURL = res.URI, res.WebURL
		if res.ThumbErr != nil {
			p.log.Warn("outreach: thumbnail upload failed, posted without it", "post_id", job.ID, "err", res.ThumbErr)
		}
	case "mastodon":
		st, err := p.masto.PostStatus(ctx, job.Body, "opencircuitsf-outreach-post-"+strconv.FormatInt(job.ID, 10))
		if err != nil {
			p.postFailed(row, job, ch, err, mastodon.IsPermanent(err))
			return
		}
		remoteID, remoteURL = st.ID, st.URL
	}

	wctx, wcancel := context.WithTimeout(context.WithoutCancel(parent), writeBudget)
	defer wcancel()
	ok, err = p.store.MarkRemotePosted(wctx, job.ID, remoteID, remoteURL, time.Now())
	switch {
	case err != nil:
		// The post exists remotely; the retry finds it (Bluesky getRecord,
		// Mastodon idempotency key) and records it without a duplicate.
		p.log.Error("outreach: posted but could not record it", "post_id", job.ID, "err", err)
		p.settle(row, "posted, but recording it failed; retrying", p.maxRetries())
		return
	case !ok:
		p.log.Warn("outreach: posted but the post was no longer queued", "post_id", job.ID)
	}
	p.markDone(row)
}

func label(ch Channel, name string) string {
	if ch.Label != "" {
		return ch.Label
	}
	return name
}

func (p *Publisher) postBluesky(ctx context.Context, job SendJob) (bluesky.PostResult, error) {
	in := bluesky.PostInput{
		Text:      job.Body,
		RKey:      bluesky.TID(job.CreatedAt.UnixMicro(), uint16(job.ID&1023)),
		CreatedAt: time.Now(),
		Embed: &bluesky.ExternalCard{
			URI: *job.ShortURL, Title: job.Title, Description: deref(job.Summary),
		},
	}
	in.Embed.Thumb, in.Embed.ThumbMIME = p.thumbnail(job.CoverImage)
	return p.bsky.Post(ctx, in)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// thumbnail loads a workshop cover from local disk only. The value must be a
// same-site /media/ path and MEDIA_DIR must be set; the file must sniff as
// JPEG or PNG and fit Bluesky's blob limit. Anything else yields no
// thumbnail and the card is sent without one. A server-side URL fetch is
// never made.
func (p *Publisher) thumbnail(cover *string) ([]byte, string) {
	if cover == nil || p.mediaDir == "" || !strings.HasPrefix(*cover, "/media/") {
		return nil, ""
	}
	name := filepath.Base(*cover)
	if name == "." || name == "/" || name == ".." || name == "" {
		return nil, ""
	}
	data, err := os.ReadFile(filepath.Join(p.mediaDir, name))
	if err != nil || len(data) == 0 || len(data) > bluesky.MaxThumbBytes {
		return nil, ""
	}
	switch f, err := media.Sniff(data); {
	case err != nil:
		return nil, ""
	case f == media.FormatJPEG:
		return data, "image/jpeg"
	case f == media.FormatPNG:
		return data, "image/png"
	}
	return nil, ""
}

// postFailed turns a client error into a user-facing, secret-free last_error
// and settles the row.
func (p *Publisher) postFailed(row outbox.Row, job SendJob, ch Channel, err error, permanent bool) {
	p.log.Warn("outreach: post attempt failed", "post_id", job.ID, "channel", ch.Name, "attempt", row.Attempts, "permanent", permanent, "err", err)
	if permanent {
		p.fail(row, job, ch.Label+" rejected the post: "+briefError(err), true)
		return
	}
	max := p.maxRetries()
	if row.Attempts >= max {
		p.fail(row, job, fmt.Sprintf("%s is unreachable or failing (attempt %d of %d); giving up: %s", ch.Label, row.Attempts, max, briefError(err)), false)
		return
	}
	p.fail(row, job, fmt.Sprintf("%s is unreachable or failing (attempt %d of %d); retrying: %s", ch.Label, row.Attempts, max, briefError(err)), false)
}

// briefError is the client error text, which is already secret-free (each
// client redacts its credentials), bounded in length.
func briefError(err error) string {
	s := err.Error()
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "..."
	}
	return s
}

// fail records msg on the post and settles the outbox row: permanent goes
// straight to abandoned and failed; otherwise the standard backoff applies
// until the retry budget is spent.
func (p *Publisher) fail(row outbox.Row, job SendJob, msg string, permanent bool) {
	max := 0
	if !permanent {
		max = p.maxRetries()
	}
	final := permanent || row.Attempts >= max
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	if _, err := p.outbox.MarkRetryOrAbandon(ctx, row.ID, row.Attempts, msg, max); err != nil {
		p.log.Error("outreach: could not settle queue row", "id", row.ID, "err", err)
	}
	if _, err := p.store.MarkSendError(ctx, job.ID, msg, final); err != nil {
		p.log.Error("outreach: could not record send error", "post_id", job.ID, "err", err)
	}
}

// settle settles a row that has no loadable post to record an error on.
func (p *Publisher) settle(row outbox.Row, msg string, max int) {
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	if _, err := p.outbox.MarkRetryOrAbandon(ctx, row.ID, row.Attempts, msg, max); err != nil {
		p.log.Error("outreach: could not settle queue row", "id", row.ID, "err", err)
	}
}

func (p *Publisher) markDone(row outbox.Row) {
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	if _, err := p.outbox.MarkDone(ctx, row.ID); err != nil {
		p.log.Error("outreach: could not close queue row", "id", row.ID, "err", err)
	}
}

// hold is the send-time re-check: the workshop was canceled or unpublished
// after the post was queued.
func (p *Publisher) hold(row outbox.Row, job SendJob) {
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	p.log.Warn("outreach: workshop not published, holding post", "post_id", job.ID, "workshop_status", job.WorkshopStatus)
	if _, err := p.outbox.MarkSkipped(ctx, row.ID, "workshop not published"); err != nil {
		p.log.Error("outreach: could not skip queue row", "id", row.ID, "err", err)
	}
	if _, err := p.store.HoldQueued(ctx, job.ID, HeldNotPublishedMessage(job.WorkshopStatus)); err != nil {
		p.log.Error("outreach: could not hold post", "post_id", job.ID, "err", err)
	}
}

// maxRetries reads settings.queue_max_retries, falling back to the outbox
// default on any read or parse failure, as the mail worker does.
func (p *Publisher) maxRetries() int {
	if p.settings == nil {
		return outbox.DefaultMaxRetries
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeBudget)
	defer cancel()
	raw, err := p.settings.GetSetting(ctx, settingQueueMaxRetries)
	if err != nil {
		p.log.Warn("outreach: settings read failed, using the default retry count", "err", err)
		return outbox.DefaultMaxRetries
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || n <= 0 {
		return outbox.DefaultMaxRetries
	}
	return n
}
