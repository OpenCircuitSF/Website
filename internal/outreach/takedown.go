package outreach

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// Reasons a takedown is refused (#0604).
var (
	// ErrTakedownNotPosted: the post is not an automatic post in status posted
	// with a remote id to delete.
	ErrTakedownNotPosted = errors.New("outreach: only a post the site sent and that is posted can be taken down")
	// ErrTakedownInFlight: a queue row for the post is still live, so a send
	// may be racing the takedown.
	ErrTakedownInFlight = errors.New("outreach: the post still has a queue row in flight")
	// ErrTakedownChanged: the post changed between the remote delete and the
	// local update (for example, a concurrent takedown already returned it to
	// draft).
	ErrTakedownChanged = errors.New("outreach: the post changed while it was being taken down")
)

// BlueskyRKey is the deterministic record key for a post's Bluesky send. The
// same post always maps to the same key, which is what lets a retry discover
// that an earlier attempt already created the record.
//
// Generation 0 is the original derivation, unchanged. Each takedown (#0604)
// adds a generation, which moves the TID's timestamp forward one second per
// generation, so a re-send after a takedown can never equal the key of the
// record that was deleted: the AppView caches by URI, and a re-created record
// at a deleted URI is fragile.
func BlueskyRKey(createdAt time.Time, postID int64, generation int) string {
	micros := createdAt.UnixMicro() + int64(generation)*int64(time.Second/time.Microsecond)
	return bluesky.TID(micros, uint16(postID&1023))
}

// MastodonIdempotencyKey is the Idempotency-Key for a post's Mastodon send.
// Generation 0 keeps the original key exactly, so in-flight retries are
// unaffected; later generations include the generation, so an instance that
// still remembers the deleted status's key (it keeps one for about an hour)
// does not hand the deleted status back.
func MastodonIdempotencyKey(postID int64, generation int) string {
	k := "opencircuitsf-outreach-post-" + strconv.FormatInt(postID, 10)
	if generation > 0 {
		k += "-g" + strconv.Itoa(generation)
	}
	return k
}

// isLinkChar reports whether c can continue a short-link key or path.
func isLinkChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '/'
}

// legacyShortURL derives the pre-#0603 form of a repaired short URL:
// https://host/u/KEY becomes https://host/KEY. ok is false when shortURL is
// not in the /u/ form.
func legacyShortURL(shortURL string) (string, bool) {
	i := strings.Index(shortURL, "://")
	if i < 0 {
		return "", false
	}
	rest := shortURL[i+3:]
	j := strings.Index(rest, "/u/")
	if j <= 0 || strings.Contains(rest[:j], "/") || len(rest) == j+3 {
		return "", false
	}
	return shortURL[:i+3] + rest[:j] + "/" + rest[j+3:], true
}

// RefreshBodyLink returns body with its dead pre-#0603 short link replaced by
// shortURL, the row's current (repaired) link. #0603 repaired short_url on
// posted rows but left their bodies as sent, so a re-sent body must be
// refreshed. The old form is matched as a whole URL, never as the prefix of a
// longer one (host/abc inside host/abcd, or host/u inside host/u/KEY). A body
// that already carries shortURL, or the {link} token, has nothing to replace
// and comes back unchanged.
func RefreshBodyLink(body, shortURL string) string {
	old, ok := legacyShortURL(shortURL)
	if !ok {
		return body
	}
	var b strings.Builder
	for i := 0; ; {
		j := strings.Index(body[i:], old)
		if j < 0 {
			b.WriteString(body[i:])
			return b.String()
		}
		end := i + j + len(old)
		b.WriteString(body[i : i+j])
		if end < len(body) && isLinkChar(body[end]) {
			b.WriteString(old) // part of a longer URL: leave it
		} else {
			b.WriteString(shortURL)
		}
		i = end
	}
}

// RemoteDeleter removes a post from its network. existed is false when the
// post was already gone, which is success.
type RemoteDeleter interface {
	DeleteRemote(ctx context.Context, p Post) (existed bool, err error)
}

// DeleteRemote deletes p's post from its network using the stored remote id.
// "Already gone" is success. Safe on a nil receiver, which reports the
// channel as not configured.
func (p *Publisher) DeleteRemote(ctx context.Context, post Post) (bool, error) {
	if p == nil {
		return false, errors.New("the site is not set up to post to this channel")
	}
	if post.RemoteID == nil || *post.RemoteID == "" {
		return false, errors.New("the post has no remote id to delete")
	}
	switch post.Channel {
	case "bluesky":
		c := p.clients.Bluesky()
		if !c.Enabled() {
			return false, errors.New("Bluesky is not configured on this server")
		}
		rkey := bluesky.RKeyFromURI(*post.RemoteID)
		if rkey == "" {
			return false, fmt.Errorf("the stored Bluesky post id %q is not recognised", *post.RemoteID)
		}
		return c.DeletePost(ctx, rkey)
	case "mastodon":
		c := p.clients.Mastodon()
		if !c.Enabled() {
			return false, errors.New("Mastodon is not configured on this server")
		}
		return c.DeleteStatus(ctx, *post.RemoteID)
	}
	return false, errors.New("this channel is not posted by the site")
}

// liveQueueRowExists reports whether an unfinished queue row exists for the
// post. A done row is history; queued and sending rows may still send.
func liveQueueRowExists(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, postID int64) (bool, error) {
	var live bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbound_queue
		WHERE kind = $1 AND (payload->>'post_id')::bigint = $2 AND status IN ('queued','sending'))`,
		string(outbox.KindOutreachPost), postID).Scan(&live)
	return live, err
}

// CheckTakedown reports whether id can be taken down, and returns the post:
// it must be posted by the site (a remote id), and have no live queue row.
// ErrNotFound, ErrTakedownNotPosted or ErrTakedownInFlight otherwise. It
// holds no lock, so the caller re-checks under CompleteTakedown's lock.
func (s *Store) CheckTakedown(ctx context.Context, id int64) (Post, error) {
	p, err := s.GetPost(ctx, id)
	if err != nil {
		return Post{}, err
	}
	if p.Status != "posted" || p.RemoteID == nil || *p.RemoteID == "" {
		return Post{}, ErrTakedownNotPosted
	}
	live, err := liveQueueRowExists(ctx, s.pool, id)
	if err != nil {
		return Post{}, fmt.Errorf("outreach: takedown check: %w", err)
	}
	if live {
		return Post{}, ErrTakedownInFlight
	}
	return p, nil
}

// CompleteTakedown returns a taken-down post to draft once its remote post is
// gone. Under a row lock it re-checks that the post is still posted with the
// same remote id and has no live queue row (ErrTakedownChanged,
// ErrTakedownInFlight), then clears posted_at, remote_id, remote_url,
// scheduled_at and last_error, adds one to attempt_generation, and refreshes
// the body's link to the row's current short_url.
func (s *Store) CompleteTakedown(ctx context.Context, id int64, remoteID string) (Post, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Post{}, fmt.Errorf("outreach: takedown: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	var curRemote, shortURL *string
	var body string
	err = tx.QueryRow(ctx, `SELECT status, remote_id, short_url, body FROM outreach_posts WHERE id = $1 FOR UPDATE`, id).
		Scan(&status, &curRemote, &shortURL, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: takedown: %w", err)
	}
	if status != "posted" || curRemote == nil || *curRemote != remoteID {
		return Post{}, ErrTakedownChanged
	}
	live, err := liveQueueRowExists(ctx, tx, id)
	if err != nil {
		return Post{}, fmt.Errorf("outreach: takedown: %w", err)
	}
	if live {
		return Post{}, ErrTakedownInFlight
	}
	if shortURL != nil {
		body = RefreshBodyLink(body, *shortURL)
	}
	p, err := scanPost(tx.QueryRow(ctx,
		`UPDATE outreach_posts SET status = 'draft', posted_at = NULL, remote_id = NULL, remote_url = NULL,
		        scheduled_at = NULL, last_error = NULL, body = $2, attempt_generation = attempt_generation + 1, updated_at = now()
		 WHERE id = $1 RETURNING `+postColumns, id, body))
	if err != nil {
		return Post{}, fmt.Errorf("outreach: takedown: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Post{}, fmt.Errorf("outreach: takedown: %w", err)
	}
	return p, nil
}
