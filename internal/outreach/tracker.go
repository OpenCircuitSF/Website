package outreach

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
)

const (
	defaultTrackerInterval = 15 * time.Minute
	// maxNotificationPages bounds one pass's walk through a channel's
	// notifications; the next pass resumes from the stored position.
	maxNotificationPages = 10
	// mentionLookback is how far back the very first pass looks.
	mentionLookback = 30 * 24 * time.Hour
	maxMentionRunes = 1000
	mastodonPage    = 80
	bskyPage        = 100
)

// TrackerDeps are the Tracker's collaborators. A nil Bluesky or Mastodon
// client switches that half off.
type TrackerDeps struct {
	Store    *Store
	Bluesky  *bluesky.Client
	Mastodon *mastodon.Client
	Interval time.Duration    // default 15 minutes
	Now      func() time.Time // default time.Now; tests pin it
	Log      *slog.Logger
}

// Tracker polls engagement counts for posted Bluesky and Mastodon posts and
// collects mentions, replies and quotes of the two accounts (#0547). The
// Publisher starts and stops it. Unlike a send, a pass can be cut short: every
// insert is idempotent and a position is stored only after the work it covers.
type Tracker struct {
	store    *Store
	bsky     *bluesky.Client
	masto    *mastodon.Client
	interval time.Duration
	now      func() time.Time
	log      *slog.Logger

	// attempted is each post's last capture attempt, so a failing or deleted
	// remote post is not re-fetched and re-logged every tick. Only pass reads
	// or writes it, and passes never overlap.
	attempted map[int64]time.Time

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
	runMu    sync.Mutex
	running  bool
	cancel   context.CancelFunc
}

// NewTracker builds a Tracker. It starts nothing.
func NewTracker(d TrackerDeps) *Tracker {
	if d.Interval <= 0 {
		d.Interval = defaultTrackerInterval
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Tracker{
		store: d.Store, bsky: d.Bluesky, masto: d.Mastodon, interval: d.Interval, now: d.Now, log: d.Log,
		attempted: map[int64]time.Time{},
		stopCh:    make(chan struct{}), doneCh: make(chan struct{}),
	}
}

// Run blocks, passing at once and then every interval, until Stop is called
// or ctx is done. With neither channel configured it returns immediately.
func (t *Tracker) Run(ctx context.Context) {
	defer close(t.doneCh)
	select {
	case <-t.stopCh:
		return
	default:
	}
	if !t.bsky.Enabled() {
		t.log.Info("outreach tracker: bluesky tracking off (not configured)")
	}
	if !t.masto.Enabled() {
		t.log.Info("outreach tracker: mastodon tracking off (not configured)")
	}
	if !t.bsky.Enabled() && !t.masto.Enabled() {
		t.log.Info("outreach tracker: no channel configured, not polling")
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t.runMu.Lock()
	t.running, t.cancel = true, cancel
	t.runMu.Unlock()
	// Stop may have run between the check above and the lock.
	select {
	case <-t.stopCh:
		return
	default:
	}
	for {
		t.pass(ctx)
		select {
		case <-t.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(t.interval):
		}
	}
}

// Stop cancels any pass in flight, tells Run to return, and waits for it or
// for ctx. Safe to call more than once and when Run was never started.
func (t *Tracker) Stop(ctx context.Context) error {
	t.stopOnce.Do(func() { close(t.stopCh) })
	t.runMu.Lock()
	running, cancel := t.running, t.cancel
	t.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if !running {
		return nil
	}
	select {
	case <-t.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pass is one mentions-then-engagement sweep. A failure in one half never
// stops the others, and the pass always ends with one summary line.
func (t *Tracker) pass(ctx context.Context) {
	var bskyIn, mastoIn int
	if t.bsky.Enabled() {
		bskyIn = t.bskyMentions(ctx)
	}
	if t.masto.Enabled() {
		mastoIn = t.mastoMentions(ctx)
	}
	written, failed := t.engagement(ctx)
	if ctx.Err() != nil {
		t.log.Info("outreach tracker: pass interrupted by shutdown")
		return
	}
	t.log.Info("outreach tracker: pass done",
		"bluesky_mentions_inserted", bskyIn, "mastodon_mentions_inserted", mastoIn,
		"snapshots_written", written, "capture_failures", failed)
}

// failed logs a failed request. A cancelled context is shutdown, not a fault.
func (t *Tracker) failed(ctx context.Context, channel, op string, err error, hint string) {
	if ctx.Err() != nil {
		t.log.Info("outreach tracker: request interrupted by shutdown", "channel", channel, "op", op)
		return
	}
	attrs := []any{"channel", channel, "op", op, "err", err}
	if hint != "" && (errors.Is(err, mastodon.ErrAuth) || errors.Is(err, bluesky.ErrAuth)) {
		attrs = append(attrs, "hint", hint)
	}
	t.log.Error("outreach tracker: request failed", attrs...)
}

// ---- mentions: Bluesky ----

func (t *Tracker) bskyMentions(ctx context.Context) (inserted int) {
	const ch = "bluesky"
	floor := t.now().Add(-mentionLookback)
	var mark time.Time
	hasMark := false
	if cur, ok, err := t.store.MentionCursor(ctx, ch); err != nil {
		t.failed(ctx, ch, "read cursor", err, "")
		return 0
	} else if ok {
		if mt, perr := time.Parse(time.RFC3339Nano, cur); perr == nil {
			floor, mark, hasMark = mt, mt, true
		} else {
			t.log.Warn("outreach tracker: stored bluesky cursor is not a timestamp, using the 30-day floor", "err", perr)
		}
	}
	remoteIDs, err := t.store.PostRemoteIDs(ctx, ch)
	if err != nil {
		t.failed(ctx, ch, "post remote ids", err, "")
		return 0
	}
	var newest time.Time
	cursor, ended := "", false
	for page := 0; page < maxNotificationPages && !ended; page++ {
		pg, err := t.bsky.ListNotifications(ctx, cursor, bskyPage)
		if err != nil {
			t.failed(ctx, ch, "list notifications", err, "app password rejected")
			return inserted
		}
		for _, n := range pg.Items {
			at, perr := time.Parse(time.RFC3339Nano, n.IndexedAt)
			if perr != nil {
				t.log.Debug("outreach tracker: notification skipped, bad indexedAt", "channel", ch, "remote_id", n.URI)
				continue
			}
			if newest.IsZero() || at.After(newest) {
				newest = at
			}
			if at.Before(floor) {
				t.log.Debug("outreach tracker: notification older than the mark ends the walk", "channel", ch, "remote_id", n.URI)
				ended = true
				break
			}
			m, ok := bskyMention(n, remoteIDs)
			if !ok {
				t.log.Debug("outreach tracker: notification skipped", "channel", ch, "remote_id", n.URI, "reason", n.Reason)
				continue
			}
			ins, err := t.store.InsertMention(ctx, m)
			if err != nil {
				t.failed(ctx, ch, "store mention", err, "")
				return inserted
			}
			if ins {
				inserted++
			}
		}
		if pg.Cursor == "" {
			ended = true
		}
		cursor = pg.Cursor
		if !ended && page == maxNotificationPages-1 {
			t.log.Warn("outreach tracker: older notifications skipped, page cap reached", "channel", ch, "pages", maxNotificationPages)
		}
	}
	if !newest.IsZero() && (!hasMark || newest.After(mark)) {
		if err := t.store.SetMentionCursor(ctx, ch, newest.UTC().Format(time.RFC3339Nano)); err != nil {
			t.failed(ctx, ch, "write cursor", err, "")
		}
	}
	return inserted
}

// bskyMention maps one notification to a Mention. ok is false for one this
// site does not keep. A reply or quote is tied to our post when its parent,
// root, quoted record or reason subject is one of ours; a plain mention never
// is.
func bskyMention(n bluesky.Notification, remoteIDs map[string]int64) (Mention, bool) {
	// Postgres rejects NUL in text columns, and JSON lets a post carry an
	// escaped one (#0555). Strip it from every string stored below, or one
	// such item fails InsertMention on every pass and stalls the walk.
	n.URI, n.AuthorHandle, n.Text = stripNUL(n.URI), stripNUL(n.AuthorHandle), stripNUL(n.Text)
	if n.URI == "" {
		return Mention{}, false
	}
	var postID *int64
	tie := func(uris ...string) {
		for _, u := range uris {
			if id, ok := remoteIDs[u]; ok && u != "" {
				postID = &id
				return
			}
		}
	}
	switch n.Reason {
	case "mention":
	case "reply":
		tie(n.ReplyParent, n.ReplyRoot, n.ReasonSubject)
	case "quote":
		tie(n.QuoteURI, n.ReasonSubject)
	default:
		return Mention{}, false
	}
	created, err := time.Parse(time.RFC3339Nano, n.CreatedAt)
	if err != nil {
		if created, err = time.Parse(time.RFC3339Nano, n.IndexedAt); err != nil {
			return Mention{}, false
		}
	}
	handle := n.AuthorHandle
	if handle == "" {
		handle = "unknown"
	}
	m := Mention{
		Channel: "bluesky", RemoteID: n.URI, PostID: postID, Kind: n.Reason,
		Author: "@" + handle, Text: capRunes(n.Text, maxMentionRunes), CreatedAt: created.UTC(),
	}
	if rkey := n.URI[strings.LastIndex(n.URI, "/")+1:]; n.AuthorHandle != "" && rkey != "" && !strings.Contains(rkey, ":") {
		m.URL = httpsURL("https://bsky.app/profile/" + url.PathEscape(n.AuthorHandle) + "/post/" + url.PathEscape(rkey))
	}
	return m, true
}

// ---- mentions: Mastodon ----

func (t *Tracker) mastoMentions(ctx context.Context) (inserted int) {
	const ch = "mastodon"
	const hint = "the token needs the read:notifications scope"
	cursor, has, err := t.store.MentionCursor(ctx, ch)
	if err != nil {
		t.failed(ctx, ch, "read cursor", err, "")
		return 0
	}
	remoteIDs, err := t.store.PostRemoteIDs(ctx, ch)
	if err != nil {
		t.failed(ctx, ch, "post remote ids", err, "")
		return 0
	}
	process := func(items []mastodon.Notification, floor time.Time) error {
		for _, n := range items {
			m, ok := mastoMention(n, remoteIDs, floor)
			if !ok {
				t.log.Debug("outreach tracker: notification skipped", "channel", ch, "remote_id", n.ID, "type", n.Type)
				continue
			}
			ins, err := t.store.InsertMention(ctx, m)
			if err != nil {
				return err
			}
			if ins {
				inserted++
			}
		}
		return nil
	}
	if !has {
		items, err := t.masto.ListNotifications(ctx, "", mastodonPage)
		if err != nil {
			t.failed(ctx, ch, "list notifications", err, hint)
			return 0
		}
		if err := process(items, t.now().Add(-mentionLookback)); err != nil {
			t.failed(ctx, ch, "store mention", err, "")
			return inserted
		}
		if len(items) > 0 && t.cursorSafe(ch, items[0].ID) {
			if err := t.store.SetMentionCursor(ctx, ch, items[0].ID); err != nil {
				t.failed(ctx, ch, "write cursor", err, "")
			}
		}
		return inserted
	}
	for page := 0; page < maxNotificationPages; page++ {
		items, err := t.masto.ListNotifications(ctx, cursor, mastodonPage)
		if err != nil {
			t.failed(ctx, ch, "list notifications", err, hint)
			return inserted
		}
		if len(items) == 0 {
			return inserted
		}
		if err := process(items, time.Time{}); err != nil {
			t.failed(ctx, ch, "store mention", err, "")
			return inserted
		}
		if !t.cursorSafe(ch, items[0].ID) {
			return inserted
		}
		cursor = items[0].ID
		if err := t.store.SetMentionCursor(ctx, ch, cursor); err != nil {
			t.failed(ctx, ch, "write cursor", err, "")
			return inserted
		}
		if len(items) < mastodonPage {
			return inserted
		}
		if page == maxNotificationPages-1 {
			t.log.Warn("outreach tracker: older notifications skipped, page cap reached", "channel", ch, "pages", maxNotificationPages)
		}
	}
	return inserted
}

// cursorSafe reports whether id can be saved as a mention cursor. An id with
// a NUL byte cannot be written to a Postgres text column, and stripping it
// would name a different notification, so it is rejected and logged and the
// stored cursor stays as it was (#0558).
func (t *Tracker) cursorSafe(ch, id string) bool {
	if !strings.ContainsRune(id, 0) {
		return true
	}
	t.log.Warn("outreach tracker: notification id contains NUL, cursor not saved", "channel", ch, "remote_id", strings.ReplaceAll(id, "\x00", `\x00`))
	return false
}

// mastoMention maps one notification to a Mention. A mention of ours that
// replies to one of our posts is a reply; a quote is tied to the post it
// quotes. floor, when non-zero, drops anything created before it.
func mastoMention(n mastodon.Notification, remoteIDs map[string]int64, floor time.Time) (Mention, bool) {
	// Postgres text columns reject NUL; remote_id and author come straight
	// from the JSON, so strip them as bskyMention does.
	n.StatusURI, n.Acct = stripNUL(n.StatusURI), stripNUL(n.Acct)
	if !n.HasStatus || n.StatusURI == "" {
		return Mention{}, false
	}
	var postID *int64
	kind := ""
	switch n.Type {
	case "quote":
		kind = "quote"
		if id, ok := remoteIDs[n.QuotedStatusID]; ok && n.QuotedStatusID != "" {
			postID = &id
		}
	case "mention":
		kind = "mention"
		if id, ok := remoteIDs[n.InReplyToID]; ok && n.InReplyToID != "" {
			kind, postID = "reply", &id
		}
	default:
		return Mention{}, false
	}
	created, err := time.Parse(time.RFC3339Nano, n.StatusCreatedAt)
	if err != nil {
		if created, err = time.Parse(time.RFC3339Nano, n.CreatedAt); err != nil {
			return Mention{}, false
		}
	}
	if !floor.IsZero() && created.Before(floor) {
		return Mention{}, false
	}
	link := n.StatusURL
	if link == "" {
		link = n.StatusURI
	}
	acct := n.Acct
	if acct == "" {
		acct = "unknown"
	}
	return Mention{
		Channel: "mastodon", RemoteID: n.StatusURI, PostID: postID, Kind: kind,
		Author: "@" + acct, Text: capRunes(mastodon.PlainText(n.ContentHTML), maxMentionRunes),
		URL: httpsURL(link), CreatedAt: created.UTC(),
	}, true
}

// ---- engagement ----

func (t *Tracker) engagement(ctx context.Context) (written, failed int) {
	var channels []string
	if t.bsky.Enabled() {
		channels = append(channels, "bluesky")
	}
	if t.masto.Enabled() {
		channels = append(channels, "mastodon")
	}
	targets, err := t.store.EngagementTargets(ctx, channels)
	if err != nil {
		t.failed(ctx, "", "engagement targets", err, "")
		return 0, 0
	}
	now := t.now()
	live := make(map[int64]bool, len(targets))
	var bskyDue, mastoDue []EngagementTarget
	for _, tg := range targets {
		live[tg.PostID] = true
		last := tg.LastCaptured
		if a, ok := t.attempted[tg.PostID]; ok && (last == nil || a.After(*last)) {
			a := a
			last = &a
		}
		if !EngagementDue(tg.PostedAt, last, now) {
			continue
		}
		if tg.Channel == "bluesky" {
			bskyDue = append(bskyDue, tg)
		} else {
			mastoDue = append(mastoDue, tg)
		}
	}
	for id := range t.attempted {
		if !live[id] {
			delete(t.attempted, id)
		}
	}
	record := func(tg EngagementTarget, c Counts) {
		if err := t.store.RecordEngagement(ctx, tg.PostID, now, c); err != nil {
			failed++
			t.failed(ctx, tg.Channel, "store snapshot", err, "")
			return
		}
		written++
	}
	gone := func(tg EngagementTarget) {
		t.log.Info("outreach tracker: remote post not found, will retry at its next due slot", "post_id", tg.PostID, "channel", tg.Channel)
	}

	if len(bskyDue) > 0 {
		uris := make([]string, len(bskyDue))
		for i, tg := range bskyDue {
			uris[i] = tg.RemoteID
			t.attempted[tg.PostID] = now
		}
		counts, err := t.bsky.GetPostCounts(ctx, uris)
		if err != nil {
			failed += len(bskyDue)
			t.failed(ctx, "bluesky", "get post counts", err, "")
		} else {
			for _, tg := range bskyDue {
				if c, ok := counts[tg.RemoteID]; ok {
					record(tg, Counts{c.Likes, c.Reposts, c.Replies, c.Quotes})
				} else {
					gone(tg)
				}
			}
		}
	}
	for _, tg := range mastoDue {
		if ctx.Err() != nil {
			break
		}
		t.attempted[tg.PostID] = now
		c, err := t.masto.GetStatusCounts(ctx, tg.RemoteID)
		switch {
		case err == nil:
			record(tg, Counts{c.Likes, c.Reposts, c.Replies, c.Quotes})
		case errors.Is(err, mastodon.ErrNotFound):
			gone(tg)
		default:
			failed++
			t.failed(ctx, "mastodon", "get status counts", err, "the token needs the read:statuses scope")
		}
	}
	return written, failed
}

// ---- helpers ----

// stripNUL removes NUL bytes, which Postgres text columns cannot hold.
func stripNUL(s string) string {
	if !strings.Contains(s, "\x00") {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// capRunes truncates s to at most n runes.
func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// httpsURL returns s only when it is an absolute https URL with a host and no
// credentials; third-party links are shown to admins and must not be anything
// else.
func httpsURL(s string) *string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil
	}
	out := u.String()
	return &out
}
