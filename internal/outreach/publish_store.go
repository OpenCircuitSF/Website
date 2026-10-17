package outreach

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// sendableStatuses are the statuses a post can be sent or scheduled from.
const sendableStatuses = `('draft','scheduled','failed')`

// publishedWorkshop is the EXISTS clause shared by every write that must
// refuse a post whose workshop is canceled or unpublished (#0545). p is the
// outreach_posts alias.
const publishedWorkshop = `EXISTS (SELECT 1 FROM outreach o JOIN workshops w ON w.id = o.workshop_id
	WHERE o.id = p.outreach_id AND w.status = 'published')`

// maxErrorChars bounds last_error; it is shown to an admin.
const maxErrorChars = 500

// HeldNotPublishedMessage is the last_error a post gets when it comes due and
// its workshop is not published. status is the workshop's status.
func HeldNotPublishedMessage(status string) string {
	what := "unpublished"
	if status == "canceled" {
		what = "canceled"
	}
	return "Held: the workshop was " + what + " when this post was due. Reschedule to send it."
}

// classifySend explains why a state-guarded send or schedule touched no row.
func (s *Store) classifySend(ctx context.Context, id int64) error {
	p, err := s.GetPost(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case p.Status == "posted":
		return ErrPostLocked
	case p.Status == "queued":
		return ErrPostInFlight
	case !p.Prepared():
		return ErrNotPrepared
	case p.Status != "draft" && p.Status != "scheduled" && p.Status != "failed":
		return ErrWrongStatus
	}
	var ws string
	err = s.pool.QueryRow(ctx, `SELECT w.status FROM outreach_posts p
		JOIN outreach o ON o.id = p.outreach_id JOIN workshops w ON w.id = o.workshop_id WHERE p.id = $1`, id).Scan(&ws)
	if err != nil {
		return fmt.Errorf("outreach: workshop status: %w", err)
	}
	if ws != "published" {
		return ErrWorkshopNotPublished
	}
	return fmt.Errorf("outreach: post %d changed concurrently", id)
}

// QueueNow moves a draft, scheduled or failed post to queued and enqueues
// one outbound_queue row for it, in one transaction. prevStatus is the status
// it had before (so the caller can tell a Retry from a first send).
func (s *Store) QueueNow(ctx context.Context, id int64) (post Post, prevStatus string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Post{}, "", fmt.Errorf("outreach: queue now: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT status FROM outreach_posts WHERE id = $1 FOR UPDATE`, id).Scan(&prevStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Post{}, "", ErrNotFound
		}
		return Post{}, "", fmt.Errorf("outreach: queue now: %w", err)
	}
	p, err := scanPost(tx.QueryRow(ctx,
		`UPDATE outreach_posts p SET status = 'queued', scheduled_at = now(), last_error = NULL, updated_at = now()
		 WHERE p.id = $1 AND p.status IN `+sendableStatuses+` AND p.short_url IS NOT NULL AND `+publishedWorkshop+`
		 RETURNING `+qualifiedPostColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Rollback(ctx)
		return Post{}, "", s.classifySend(ctx, id)
	}
	if err != nil {
		return Post{}, "", fmt.Errorf("outreach: queue now: %w", err)
	}
	if _, err := s.enqueueTx(ctx, tx, p.ID); err != nil {
		return Post{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Post{}, "", fmt.Errorf("outreach: queue now: %w", err)
	}
	return p, prevStatus, nil
}

// qualifiedPostColumns is postColumns with the p alias, for statements that
// alias the table.
const qualifiedPostColumns = `p.id, p.outreach_id, p.channel, p.wave, p.body, p.status, p.source_fingerprint,
	p.short_link_key, p.short_url, p.remote_id, p.remote_url, p.last_error,
	p.scheduled_at, p.posted_at, p.updated_at, p.created_at`

func (s *Store) enqueueTx(ctx context.Context, tx pgx.Tx, postID int64) (int64, error) {
	return s.outbox.EnqueueTx(ctx, tx, outbox.Item{
		Kind:      outbox.KindOutreachPost,
		Recipient: fmt.Sprintf("outreach_post:%d", postID), // a label, not an address
		Payload:   map[string]any{"post_id": postID},
	})
}

// Schedule sets a prepared draft, scheduled or failed post to go out at.
// Nothing is queued yet: the publisher's promoter enqueues it when due.
func (s *Store) Schedule(ctx context.Context, id int64, at time.Time) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE outreach_posts p SET status = 'scheduled', scheduled_at = $2, last_error = NULL, updated_at = now()
		 WHERE p.id = $1 AND p.status IN `+sendableStatuses+` AND p.short_url IS NOT NULL AND `+publishedWorkshop+`
		 RETURNING `+qualifiedPostColumns, id, at))
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, s.classifySend(ctx, id)
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: schedule: %w", err)
	}
	return p, nil
}

// Unschedule returns a scheduled post to draft. ErrWrongStatus otherwise.
func (s *Store) Unschedule(ctx context.Context, id int64) (Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx,
		`UPDATE outreach_posts SET status = 'draft', scheduled_at = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'scheduled' RETURNING `+postColumns, id))
	if errors.Is(err, pgx.ErrNoRows) {
		cur, gerr := s.GetPost(ctx, id)
		if gerr != nil {
			return Post{}, gerr
		}
		if cur.Status == "queued" {
			return Post{}, ErrPostInFlight
		}
		return Post{}, ErrWrongStatus
	}
	if err != nil {
		return Post{}, fmt.Errorf("outreach: unschedule: %w", err)
	}
	return p, nil
}

// PromoteDue turns scheduled posts whose time has come into queued posts, one
// outbound_queue row each, in one transaction. A due post whose workshop is
// not published is not queued: it goes back to draft with an explanatory
// last_error, so republishing the workshop never fires a stale burst.
func (s *Store) PromoteDue(ctx context.Context, limit int) (promoted, held int, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`SELECT p.id, w.status FROM outreach_posts p
		   JOIN outreach o ON o.id = p.outreach_id JOIN workshops w ON w.id = o.workshop_id
		  WHERE p.status = 'scheduled' AND p.scheduled_at <= now()
		  ORDER BY p.scheduled_at, p.id LIMIT $1 FOR UPDATE OF p SKIP LOCKED`, limit)
	if err != nil {
		return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
	}
	type due struct {
		id     int64
		wstate string
	}
	var batch []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.wstate); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
		}
		batch = append(batch, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
	}
	for _, d := range batch {
		if d.wstate == "published" {
			if _, err := tx.Exec(ctx, `UPDATE outreach_posts SET status = 'queued', last_error = NULL, updated_at = now() WHERE id = $1`, d.id); err != nil {
				return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
			}
			if _, err := s.enqueueTx(ctx, tx, d.id); err != nil {
				return 0, 0, err
			}
			promoted++
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE outreach_posts SET status = 'draft', scheduled_at = NULL, last_error = $2, updated_at = now() WHERE id = $1`,
			d.id, HeldNotPublishedMessage(d.wstate)); err != nil {
			return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
		}
		held++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("outreach: promote due: %w", err)
	}
	return promoted, held, nil
}

// SendJob is a post joined to the workshop facts the publisher needs.
type SendJob struct {
	Post
	WorkshopStatus string
	Title          string
	Slug           string
	Summary        *string
	CoverImage     *string
}

// LoadForSend re-reads a post and its workshop at send time. ErrNotFound when
// the post is gone.
func (s *Store) LoadForSend(ctx context.Context, postID int64) (SendJob, error) {
	var j SendJob
	err := s.pool.QueryRow(ctx,
		`SELECT `+qualifiedPostColumns+`, w.status, w.title, w.slug, w.summary, w.cover_image
		   FROM outreach_posts p JOIN outreach o ON o.id = p.outreach_id JOIN workshops w ON w.id = o.workshop_id
		  WHERE p.id = $1`, postID).Scan(
		&j.ID, &j.OutreachID, &j.Channel, &j.Wave, &j.Body, &j.Status, &j.SourceFingerprint,
		&j.ShortLinkKey, &j.ShortURL, &j.RemoteID, &j.RemoteURL, &j.LastError,
		&j.ScheduledAt, &j.PostedAt, &j.UpdatedAt, &j.CreatedAt,
		&j.WorkshopStatus, &j.Title, &j.Slug, &j.Summary, &j.CoverImage)
	if errors.Is(err, pgx.ErrNoRows) {
		return SendJob{}, ErrNotFound
	}
	if err != nil {
		return SendJob{}, fmt.Errorf("outreach: load for send: %w", err)
	}
	return j, nil
}

// MarkRemotePosted records a successful automatic post. false means the post
// was no longer queued (edited or deleted meanwhile).
func (s *Store) MarkRemotePosted(ctx context.Context, id int64, remoteID, remoteURL string, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outreach_posts SET status = 'posted', posted_at = $4, remote_id = $2, remote_url = $3, last_error = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'queued'`, id, remoteID, remoteURL, now)
	if err != nil {
		return false, fmt.Errorf("outreach: mark remote posted: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkSendError records a failed attempt on a queued post. final moves it to
// failed (offering Retry); otherwise it stays queued awaiting the retry.
func (s *Store) MarkSendError(ctx context.Context, id int64, msg string, final bool) (bool, error) {
	if r := []rune(msg); len(r) > maxErrorChars {
		msg = string(r[:maxErrorChars])
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE outreach_posts SET status = CASE WHEN $3 THEN 'failed' ELSE status END, last_error = $2, updated_at = now()
		 WHERE id = $1 AND status = 'queued'`, id, msg, final)
	if err != nil {
		return false, fmt.Errorf("outreach: mark send error: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// HoldQueued returns a queued post to draft with msg, when the workshop was
// canceled or unpublished after it was queued.
func (s *Store) HoldQueued(ctx context.Context, id int64, msg string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE outreach_posts SET status = 'draft', scheduled_at = NULL, last_error = $2, updated_at = now()
		 WHERE id = $1 AND status = 'queued'`, id, msg)
	if err != nil {
		return false, fmt.Errorf("outreach: hold queued: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
