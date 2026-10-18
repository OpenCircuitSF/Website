package outreach

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

var (
	// ErrNotFound is returned when no row matches a lookup.
	ErrNotFound = errors.New("outreach: not found")
	// ErrPostExists is returned when a (outreach, channel, wave) slot is taken.
	ErrPostExists = errors.New("outreach: a post already exists for that channel and wave")
	// ErrPostLocked is returned when a write is refused because the post is
	// already posted.
	ErrPostLocked = errors.New("outreach: post is already posted")
	// ErrNotPrepared is returned when an action needs the post's short link
	// and Prepare has not run yet.
	ErrNotPrepared = errors.New("outreach: post has no short link yet")
	// ErrPostInFlight is returned when a write is refused because the post is
	// queued for sending (#0545): it is locked from the moment it is queued
	// until it is posted or fails.
	ErrPostInFlight = errors.New("outreach: post is being sent")
	// ErrWrongStatus is returned when a send or schedule is asked of a post
	// whose status cannot take it (for example a manual-channel post).
	ErrWrongStatus = errors.New("outreach: post is not in a status that can be sent")
	// ErrWorkshopNotPublished is returned when a send or schedule is asked of
	// a post whose workshop is canceled or unpublished (#0545).
	ErrWorkshopNotPublished = errors.New("outreach: the workshop is not published")
)

// Outreach is one row of the outreach table.
type Outreach struct {
	ID           int64
	WorkshopID   int64
	CampaignSlug string
	CreatedAt    time.Time
	UpdatedAt    *time.Time
	ArchivedAt   *time.Time
}

// Post is one row of the outreach_posts table.
type Post struct {
	ID                int64
	OutreachID        int64
	Channel           string
	Wave              string
	Body              string
	Status            string
	SourceFingerprint string
	// Customized marks a hand-edited automatic post that a shared-message save
	// leaves alone (#0582). A regenerate clears it.
	Customized   bool
	ShortLinkKey *string
	ShortURL     *string
	RemoteID     *string
	RemoteURL    *string
	LastError    *string
	ScheduledAt  *time.Time
	PostedAt     *time.Time
	UpdatedAt    *time.Time
	CreatedAt    time.Time
}

// Prepared reports whether the post's short link exists.
func (p Post) Prepared() bool { return p.ShortURL != nil }

// Summary is one row of the top-level Outreach tab.
type Summary struct {
	OutreachID     int64
	WorkshopID     int64
	WorkshopSlug   string
	WorkshopTitle  string
	WorkshopStatus string
	CampaignSlug   string
	StartsAt       *time.Time
	Counts         map[string]int
	CreatedAt      time.Time
}

// Store is the data-access layer over outreach and outreach_posts.
type Store struct {
	pool   *pgxpool.Pool
	outbox *outbox.Store // queue rows for automatic posting (#0545)
}

// NewStore constructs a Store over the shared pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, outbox: outbox.NewStore(pool)} }

const outreachColumns = `id, workshop_id, shortlinks_campaign_slug, created_at, updated_at, archived_at`

func scanOutreach(row pgx.Row) (Outreach, error) {
	var o Outreach
	err := row.Scan(&o.ID, &o.WorkshopID, &o.CampaignSlug, &o.CreatedAt, &o.UpdatedAt, &o.ArchivedAt)
	return o, err
}

const postColumns = `id, outreach_id, channel, wave, body, status, source_fingerprint, customized,
	short_link_key, short_url, remote_id, remote_url, last_error,
	scheduled_at, posted_at, updated_at, created_at`

func scanPost(row pgx.Row) (Post, error) {
	var p Post
	err := row.Scan(&p.ID, &p.OutreachID, &p.Channel, &p.Wave, &p.Body, &p.Status, &p.SourceFingerprint, &p.Customized,
		&p.ShortLinkKey, &p.ShortURL, &p.RemoteID, &p.RemoteURL, &p.LastError,
		&p.ScheduledAt, &p.PostedAt, &p.UpdatedAt, &p.CreatedAt)
	return p, err
}

// GetByWorkshop returns the workshop's outreach, or ErrNotFound.
func (s *Store) GetByWorkshop(ctx context.Context, workshopID int64) (Outreach, error) {
	o, err := scanOutreach(s.pool.QueryRow(ctx, `SELECT `+outreachColumns+` FROM outreach WHERE workshop_id = $1`, workshopID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Outreach{}, ErrNotFound
	}
	if err != nil {
		return Outreach{}, fmt.Errorf("outreach: get by workshop: %w", err)
	}
	return o, nil
}

// GetByID returns an outreach row, or ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id int64) (Outreach, error) {
	o, err := scanOutreach(s.pool.QueryRow(ctx, `SELECT `+outreachColumns+` FROM outreach WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Outreach{}, ErrNotFound
	}
	if err != nil {
		return Outreach{}, fmt.Errorf("outreach: get: %w", err)
	}
	return o, nil
}

// Create starts outreach for a workshop. It is idempotent: when a row exists
// it is returned with created=false.
func (s *Store) Create(ctx context.Context, workshopID int64, campaignSlug string) (Outreach, bool, error) {
	o, err := scanOutreach(s.pool.QueryRow(ctx,
		`INSERT INTO outreach (workshop_id, shortlinks_campaign_slug) VALUES ($1, $2)
		 ON CONFLICT (workshop_id) DO NOTHING RETURNING `+outreachColumns, workshopID, campaignSlug))
	if err == nil {
		return o, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Outreach{}, false, fmt.Errorf("outreach: create: %w", err)
	}
	o, err = s.GetByWorkshop(ctx, workshopID)
	return o, false, err
}

// ListSummaries returns every outreach with its workshop and per-status post
// counts, newest workshop start first (undated last).
func (s *Store) ListSummaries(ctx context.Context) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.workshop_id, w.slug, w.title, w.status, o.shortlinks_campaign_slug, w.starts_at, o.created_at
		FROM outreach o JOIN workshops w ON w.id = o.workshop_id
		ORDER BY w.starts_at DESC NULLS LAST, o.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("outreach: list summaries: %w", err)
	}
	defer rows.Close()
	out := []Summary{}
	idx := map[int64]int{}
	for rows.Next() {
		var sm Summary
		if err := rows.Scan(&sm.OutreachID, &sm.WorkshopID, &sm.WorkshopSlug, &sm.WorkshopTitle, &sm.WorkshopStatus, &sm.CampaignSlug, &sm.StartsAt, &sm.CreatedAt); err != nil {
			return nil, fmt.Errorf("outreach: scan summary: %w", err)
		}
		sm.Counts = map[string]int{}
		idx[sm.OutreachID] = len(out)
		out = append(out, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outreach: list summaries: %w", err)
	}
	crows, err := s.pool.Query(ctx, `SELECT outreach_id, status, count(*) FROM outreach_posts GROUP BY outreach_id, status`)
	if err != nil {
		return nil, fmt.Errorf("outreach: count posts: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var oid int64
		var status string
		var n int
		if err := crows.Scan(&oid, &status, &n); err != nil {
			return nil, fmt.Errorf("outreach: scan count: %w", err)
		}
		if i, ok := idx[oid]; ok {
			out[i].Counts[status] = n
		}
	}
	return out, crows.Err()
}

// ListPosts returns an outreach's posts in channel/wave insertion order.
func (s *Store) ListPosts(ctx context.Context, outreachID int64) ([]Post, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+postColumns+` FROM outreach_posts WHERE outreach_id = $1 ORDER BY id`, outreachID)
	if err != nil {
		return nil, fmt.Errorf("outreach: list posts: %w", err)
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("outreach: scan post: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPost returns one post, or ErrNotFound.
func (s *Store) GetPost(ctx context.Context, id int64) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx, `SELECT `+postColumns+` FROM outreach_posts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: get post: %w", err)
	}
	return p, nil
}

// CreatePost inserts a draft. ErrPostExists on a taken slot.
func (s *Store) CreatePost(ctx context.Context, outreachID int64, channel, wave, body, fingerprint string) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`INSERT INTO outreach_posts (outreach_id, channel, wave, body, source_fingerprint)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+postColumns, outreachID, channel, wave, body, fingerprint))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Post{}, ErrPostExists
		}
		return Post{}, fmt.Errorf("outreach: create post: %w", err)
	}
	return p, nil
}

// classify explains why a state-guarded write touched no row.
func (s *Store) classify(ctx context.Context, id int64, needPrepared bool) error {
	p, err := s.GetPost(ctx, id)
	if err != nil {
		return err
	}
	if p.Status == "posted" {
		return ErrPostLocked
	}
	if p.Status == "queued" {
		return ErrPostInFlight
	}
	if needPrepared && !p.Prepared() {
		return ErrNotPrepared
	}
	return fmt.Errorf("outreach: post %d changed concurrently", id)
}

// UpdatePostBody replaces the body. A nil fingerprint leaves the stored one
// alone (a hand edit); a non-nil one is a regenerate, which also clears
// customized. A hand edit sets customized only when asked (customized
// non-nil); nil leaves it. ErrPostLocked once posted.
func (s *Store) UpdatePostBody(ctx context.Context, id int64, body string, fingerprint *string, customized *bool) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE outreach_posts SET body = $2, source_fingerprint = COALESCE($3, source_fingerprint),
		   customized = CASE WHEN $3::text IS NOT NULL THEN false ELSE COALESCE($4::boolean, customized) END,
		   updated_at = now()
		 WHERE id = $1 AND status NOT IN ('posted','queued') RETURNING `+postColumns, id, body, fingerprint, customized))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, s.classify(ctx, id, false)
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: update post: %w", err)
	}
	return p, nil
}

// noLinkChannels are the channels whose drafts deliberately carry no link
// (Bluesky: the card holds it; Instagram: the bio does), so SetPrepared must
// not append one to a body without the token.
var noLinkChannels = []string{"bluesky", "instagram"}

// SetPrepared records the post's short link and substitutes the real URL for
// LinkToken in the body (appending it when the body has no token, except on
// the channels whose drafts carry no link, #0570), atomically
// with the write. status is the status to store (manual channels pass
// "manual"). It is idempotent: an already-prepared post is returned
// unchanged. The bool is true only when THIS call's UPDATE changed the row,
// so of two racing callers exactly one sees true; a caller that audits the
// preparation must do so only then. (Comparing the stored key to the one the
// caller holds does not work: ShortLinks de-duplicates the link, so racing
// callers hold the same key.)
func (s *Store) SetPrepared(ctx context.Context, id int64, key, shortURL, status string) (Post, bool, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE outreach_posts SET
		   short_link_key = $2, short_url = $3, status = $4,
		   body = CASE WHEN position('{link}' in body) > 0 THEN replace(body, '{link}', $3)
		               WHEN channel = ANY($5) THEN body
               ELSE body || E'\n\n' || $3 END,
		   updated_at = now()
		 WHERE id = $1 AND short_link_key IS NULL AND status <> 'posted' RETURNING `+postColumns, id, key, shortURL, status, noLinkChannels))
	if err == nil {
		return p, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Post{}, false, fmt.Errorf("outreach: set prepared: %w", err)
	}
	cur, gerr := s.GetPost(ctx, id)
	if gerr != nil {
		return Post{}, false, gerr
	}
	if cur.Prepared() {
		return cur, false, nil
	}
	return Post{}, false, ErrPostLocked
}

// MarkPosted records a manual post. ErrNotPrepared before Prepare,
// ErrPostLocked when already posted, ErrPostInFlight while queued.
func (s *Store) MarkPosted(ctx context.Context, id int64, remoteURL *string, now time.Time) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE outreach_posts SET status = 'posted', posted_at = $3, remote_url = $2, updated_at = now()
		 WHERE id = $1 AND status NOT IN ('posted','queued') AND short_url IS NOT NULL RETURNING `+postColumns, id, remoteURL, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, s.classify(ctx, id, true)
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: mark posted: %w", err)
	}
	return p, nil
}

// MarkUnposted reverts a post marked posted back to its prepared manual state
// (#0588): status 'manual', no posted_at, no remote URL. Only a posted post
// qualifies; ErrWrongStatus otherwise. Whether the channel allows it is the
// caller's rule, since a post the site itself sent is really public.
func (s *Store) MarkUnposted(ctx context.Context, id int64) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE outreach_posts SET status = 'manual', posted_at = NULL, remote_url = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'posted' AND short_url IS NOT NULL RETURNING `+postColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := s.GetPost(ctx, id); gerr != nil {
			return Post{}, gerr
		}
		return Post{}, ErrWrongStatus
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: mark unposted: %w", err)
	}
	return p, nil
}

// DeletePost removes a post that has not been posted and is not queued.
func (s *Store) DeletePost(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM outreach_posts WHERE id = $1 AND status NOT IN ('posted','queued')`, id)
	if err != nil {
		return fmt.Errorf("outreach: delete post: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return s.classify(ctx, id, false)
	}
	return nil
}
