package guideposts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// sendableStatuses are the statuses a post can be sent or scheduled from.
const sendableStatuses = `('draft','scheduled','failed')`

// maxErrorChars bounds last_error; it is shown to an admin.
const maxErrorChars = 500

// HeldUnknownGuideMessage is the last_error of a due post whose guide is no
// longer a topic on the site.
const HeldUnknownGuideMessage = "Held: this guide no longer exists on the site."

const postColumns = `id, guide_slug, channel, body, destination_url, campaign_slug, short_link_key, short_url,
	status, scheduled_at, posted_at, remote_id, remote_url, last_error, attempt_generation, created_at, updated_at`

func scanPost(row pgx.Row) (Post, error) {
	var p Post
	err := row.Scan(&p.ID, &p.GuideSlug, &p.Channel, &p.Body, &p.DestinationURL, &p.CampaignSlug, &p.ShortLinkKey, &p.ShortURL,
		&p.Status, &p.ScheduledAt, &p.PostedAt, &p.RemoteID, &p.RemoteURL, &p.LastError, &p.AttemptGeneration, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// Store is the data layer over guide_posts.
type Store struct {
	pool   *pgxpool.Pool
	outbox *outbox.Store
}

// NewStore builds a Store.
func NewStore(pool *pgxpool.Pool, ob *outbox.Store) *Store {
	return &Store{pool: pool, outbox: ob}
}

// List returns every post, newest first.
func (s *Store) List(ctx context.Context) ([]Post, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+postColumns+` FROM guide_posts ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("guideposts: list: %w", err)
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("guideposts: list: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns one post, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx, `SELECT `+postColumns+` FROM guide_posts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, fmt.Errorf("guideposts: get: %w", err)
	}
	return p, nil
}

// Create inserts a draft for the topic guide slug on channel. An empty body
// starts from DefaultBody; an empty campaign is CampaignSlug. base is the
// site's BASE_URL, used for the destination URL. The draft has no short link
// yet; Linker.Ensure mints it.
func (s *Store) Create(ctx context.Context, slug, channel, body, base, campaign string) (Post, error) {
	g, ok := topic(slug)
	if !ok {
		return Post{}, ErrUnknownGuide
	}
	ch, ok := automaticChannel(channel)
	if !ok {
		return Post{}, ErrChannelUnknown
	}
	campaign, err := NormalizeCampaignSlug(campaign)
	if err != nil {
		return Post{}, err
	}
	if strings.TrimSpace(body) == "" {
		body = DefaultBody(g)
	}
	if err := ValidateDraft(ch, body); err != nil {
		return Post{}, &BodyError{Err: err}
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT nextval('guide_posts_id_seq')`).Scan(&id); err != nil {
		return Post{}, fmt.Errorf("guideposts: create: %w", err)
	}
	p, err := scanPost(s.pool.QueryRow(ctx,
		`INSERT INTO guide_posts (id, guide_slug, channel, body, destination_url, campaign_slug)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+postColumns,
		id, g.Slug, ch.Name, body, DestinationURL(base, g.Slug, ch, id), campaign))
	if err != nil {
		return Post{}, fmt.Errorf("guideposts: create: %w", err)
	}
	return p, nil
}

// UpdateBody replaces a draft, scheduled or failed post's text. A linked
// post is measured as sent; an unlinked one with its token as a short URL.
// On a linked post the link token means the post's own short link and is
// filled in before the checks (#0676).
func (s *Store) UpdateBody(ctx context.Context, id int64, body string) (Post, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Post{}, err
	}
	if err := lockedStatus(cur.Status); err != nil {
		return Post{}, err
	}
	if cur.ShortURL != nil {
		body = strings.ReplaceAll(body, outreach.LinkToken, *cur.ShortURL)
	}
	if cur.ShortURL != nil && !containsURL(body, *cur.ShortURL) {
		return Post{}, ErrLinkRemoved
	}
	ch, _ := outreach.ChannelByName(cur.Channel)
	if cur.ShortURL != nil {
		err = outreach.ValidateForSend(ch, body)
	} else {
		err = ValidateDraft(ch, body)
	}
	if err != nil {
		return Post{}, &BodyError{Err: err}
	}
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE guide_posts SET body = $2, updated_at = now() WHERE id = $1 AND status IN `+sendableStatuses+` RETURNING `+postColumns, id, body))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, s.classify(ctx, id)
	}
	if err != nil {
		return Post{}, fmt.Errorf("guideposts: update body: %w", err)
	}
	return p, nil
}

// UpdateCampaign changes an unlinked draft's ShortLinks campaign; campaign
// follows Create's rule (blank is CampaignSlug). Once the post has its short
// link the campaign is fixed: ErrLinkFixed.
func (s *Store) UpdateCampaign(ctx context.Context, id int64, campaign string) (Post, error) {
	campaign, err := NormalizeCampaignSlug(campaign)
	if err != nil {
		return Post{}, err
	}
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE guide_posts SET campaign_slug = $2, updated_at = now()
		 WHERE id = $1 AND status = 'draft' AND short_link_key IS NULL RETURNING `+postColumns, id, campaign))
	if errors.Is(err, pgx.ErrNoRows) {
		cur, gerr := s.Get(ctx, id)
		if gerr != nil {
			return Post{}, gerr
		}
		if lerr := lockedStatus(cur.Status); lerr != nil {
			return Post{}, lerr
		}
		if cur.ShortLinkKey != nil {
			return Post{}, ErrLinkFixed
		}
		return Post{}, ErrWrongStatus
	}
	if err != nil {
		return Post{}, fmt.Errorf("guideposts: update campaign: %w", err)
	}
	return p, nil
}

// CopyAsDraft is "schedule again": a new draft from posted post id, with the
// same text and the old short URL swapped back to the link token, so the copy
// gets its own link in the same place. campaign follows Create's rule.
func (s *Store) CopyAsDraft(ctx context.Context, id int64, base, campaign string) (Post, error) {
	src, err := s.Get(ctx, id)
	if err != nil {
		return Post{}, err
	}
	if src.Status != "posted" {
		return Post{}, ErrWrongStatus
	}
	body := src.Body
	if src.ShortURL != nil {
		body = strings.ReplaceAll(body, *src.ShortURL, outreach.LinkToken)
	}
	return s.Create(ctx, src.GuideSlug, src.Channel, body, base, campaign)
}

func lockedStatus(status string) error {
	switch status {
	case "posted":
		return ErrPostLocked
	case "queued":
		return ErrPostInFlight
	}
	return nil
}

// SetLink stores a draft's short link and puts its URL in the body: in place
// of the link token, or appended after a blank line when the token was
// removed. changed is false when the post already had a link (a racing
// caller stored it first) or is no longer a draft; the current row is
// returned either way.
func (s *Store) SetLink(ctx context.Context, id int64, key, shortURL, campaign string) (Post, bool, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE guide_posts SET short_link_key = $2, short_url = $3, campaign_slug = $4,
		    body = CASE WHEN position($5 in body) > 0 THEN replace(body, $5, $3) ELSE body || E'\n\n' || $3 END,
		    updated_at = now()
		 WHERE id = $1 AND short_url IS NULL AND status = 'draft' RETURNING `+postColumns,
		id, key, shortURL, campaign, outreach.LinkToken))
	if errors.Is(err, pgx.ErrNoRows) {
		cur, gerr := s.Get(ctx, id)
		return cur, false, gerr
	}
	if err != nil {
		return Post{}, false, fmt.Errorf("guideposts: set link: %w", err)
	}
	return p, true, nil
}

// classify explains why a state-guarded write touched no row.
func (s *Store) classify(ctx context.Context, id int64) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := lockedStatus(p.Status); err != nil {
		return err
	}
	if p.ShortURL == nil {
		return ErrNoLink
	}
	return ErrWrongStatus
}

// Schedule sets a linked draft, scheduled or failed post to go out at. Nothing
// is queued yet: the publisher promotes it when due.
func (s *Store) Schedule(ctx context.Context, id int64, at time.Time) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE guide_posts SET status = 'scheduled', scheduled_at = $2, last_error = NULL, updated_at = now()
		 WHERE id = $1 AND status IN `+sendableStatuses+` AND short_url IS NOT NULL RETURNING `+postColumns, id, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, s.classify(ctx, id)
	}
	if err != nil {
		return Post{}, fmt.Errorf("guideposts: schedule: %w", err)
	}
	return p, nil
}

// QueueNow moves a linked draft, scheduled or failed post to queued and
// enqueues its outbound_queue row in the same transaction. prevStatus tells
// a Retry from a first send.
func (s *Store) QueueNow(ctx context.Context, id int64) (post Post, prevStatus string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Post{}, "", fmt.Errorf("guideposts: queue now: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT status FROM guide_posts WHERE id = $1 FOR UPDATE`, id).Scan(&prevStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Post{}, "", ErrNotFound
		}
		return Post{}, "", fmt.Errorf("guideposts: queue now: %w", err)
	}
	p, err := scanPost(tx.QueryRow(ctx,
		`UPDATE guide_posts SET status = 'queued', scheduled_at = now(), last_error = NULL, updated_at = now()
		 WHERE id = $1 AND status IN `+sendableStatuses+` AND short_url IS NOT NULL RETURNING `+postColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		return Post{}, "", s.classify(ctx, id)
	}
	if err != nil {
		return Post{}, "", fmt.Errorf("guideposts: queue now: %w", err)
	}
	if _, err := s.enqueueTx(ctx, tx, p.ID); err != nil {
		return Post{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Post{}, "", fmt.Errorf("guideposts: queue now: %w", err)
	}
	return p, prevStatus, nil
}

// Recipient is the outbound_queue label for post id.
func Recipient(id int64) string { return fmt.Sprintf("guide_post:%d", id) }

func (s *Store) enqueueTx(ctx context.Context, tx pgx.Tx, id int64) (int64, error) {
	return s.outbox.EnqueueTx(ctx, tx, outbox.Item{
		Kind:      outbox.KindGuidePost,
		Recipient: Recipient(id), // a label, not an address
		Payload:   map[string]int64{"post_id": id},
	})
}

// Unschedule returns a scheduled post to draft.
func (s *Store) Unschedule(ctx context.Context, id int64) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE guide_posts SET status = 'draft', scheduled_at = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'scheduled' RETURNING `+postColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		cur, gerr := s.Get(ctx, id)
		if gerr != nil {
			return Post{}, gerr
		}
		if cur.Status == "queued" {
			return Post{}, ErrPostInFlight
		}
		return Post{}, ErrWrongStatus
	}
	if err != nil {
		return Post{}, fmt.Errorf("guideposts: unschedule: %w", err)
	}
	return p, nil
}

// Delete removes a post that is not queued or posted.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM guide_posts WHERE id = $1 AND status NOT IN ('queued','posted')`, id)
	if err != nil {
		return fmt.Errorf("guideposts: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.classify(ctx, id)
	}
	return nil
}

// PromoteDue queues scheduled posts whose time has come, one outbound_queue
// row each, in one transaction. A post whose guide is no longer a topic goes
// back to draft with HeldUnknownGuideMessage.
func (s *Store) PromoteDue(ctx context.Context, limit int) (promoted, held int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`SELECT id, guide_slug FROM guide_posts WHERE status = 'scheduled' AND scheduled_at <= now()
		  ORDER BY scheduled_at, id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
	}
	type due struct {
		id   int64
		slug string
	}
	var batch []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.slug); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
		}
		batch = append(batch, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
	}
	for _, d := range batch {
		if _, ok := topic(d.slug); ok {
			if _, err := tx.Exec(ctx, `UPDATE guide_posts SET status = 'queued', last_error = NULL, updated_at = now() WHERE id = $1`, d.id); err != nil {
				return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
			}
			if _, err := s.enqueueTx(ctx, tx, d.id); err != nil {
				return 0, 0, err
			}
			promoted++
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE guide_posts SET status = 'draft', scheduled_at = NULL, last_error = $2, updated_at = now() WHERE id = $1`,
			d.id, HeldUnknownGuideMessage); err != nil {
			return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
		}
		held++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("guideposts: promote due: %w", err)
	}
	return promoted, held, nil
}

// LoadForSend re-reads a post at send time. ErrNotFound when it is gone.
func (s *Store) LoadForSend(ctx context.Context, id int64) (Post, error) {
	return s.Get(ctx, id)
}

// MarkRemotePosted records a successful post. false means it was no longer
// queued.
func (s *Store) MarkRemotePosted(ctx context.Context, id int64, remoteID, remoteURL string, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE guide_posts SET status = 'posted', posted_at = $4, remote_id = $2, remote_url = $3, last_error = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'queued'`, id, remoteID, remoteURL, now)
	if err != nil {
		return false, fmt.Errorf("guideposts: mark remote posted: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkSendError records a failed attempt on a queued post. final moves it to
// failed; otherwise it stays queued awaiting the retry.
func (s *Store) MarkSendError(ctx context.Context, id int64, msg string, final bool) (bool, error) {
	if r := []rune(msg); len(r) > maxErrorChars {
		msg = string(r[:maxErrorChars])
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE guide_posts SET status = CASE WHEN $3 THEN 'failed' ELSE status END, last_error = $2, updated_at = now()
		 WHERE id = $1 AND status = 'queued'`, id, msg, final)
	if err != nil {
		return false, fmt.Errorf("guideposts: mark send error: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CampaignKeys is one ShortLinks campaign's linked posts, for reading clicks:
// the campaign's short-link keys and the earliest post's creation time, the
// start of the stats window.
type CampaignKeys struct {
	CampaignSlug string
	Keys         []string
	Since        time.Time
}

// ClickKeys groups every linked post's key by campaign slug, in slug order.
func (s *Store) ClickKeys(ctx context.Context) ([]CampaignKeys, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT campaign_slug, array_agg(short_link_key ORDER BY id), min(created_at)
		   FROM guide_posts WHERE short_link_key IS NOT NULL
		  GROUP BY campaign_slug ORDER BY campaign_slug`)
	if err != nil {
		return nil, fmt.Errorf("guideposts: click keys: %w", err)
	}
	defer rows.Close()
	out := []CampaignKeys{}
	for rows.Next() {
		var c CampaignKeys
		if err := rows.Scan(&c.CampaignSlug, &c.Keys, &c.Since); err != nil {
			return nil, fmt.Errorf("guideposts: click keys: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
