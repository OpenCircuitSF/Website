package outreach

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// Reasons Reset refuses (#0592). Each means something for the workshop has
// already gone public, or is about to.
var (
	// ErrResetPosted: a post is posted (automatic or marked posted by hand).
	ErrResetPosted = errors.New("outreach: a post has already been posted")
	// ErrResetInFlight: a post is queued or being sent.
	ErrResetInFlight = errors.New("outreach: a post is queued or being sent")
	// ErrResetFlyerPutUp: a flyer link is marked put up.
	ErrResetFlyerPutUp = errors.New("outreach: a flyer has been marked put up")
)

// ResetResult counts what a reset removed.
type ResetResult struct {
	Posts  int
	Flyers int
}

// resetBlock reports why an outreach cannot be reset, or nil. It reads
// through q so Reset can run it inside its transaction.
func resetBlock(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, outreachID int64) error {
	var posted, inFlight bool
	err := q.QueryRow(ctx, `
		SELECT
		  EXISTS (SELECT 1 FROM outreach_posts WHERE outreach_id = $1 AND (status = 'posted' OR posted_at IS NOT NULL)),
		  EXISTS (SELECT 1 FROM outreach_posts WHERE outreach_id = $1 AND status = 'queued')
		  OR EXISTS (SELECT 1 FROM outbound_queue q JOIN outreach_posts p ON p.id = (q.payload->>'post_id')::bigint
		             WHERE q.kind = $2 AND p.outreach_id = $1 AND q.status IN ('queued','sending'))`,
		outreachID, string(outbox.KindOutreachPost)).Scan(&posted, &inFlight)
	if err != nil {
		return fmt.Errorf("outreach: reset check: %w", err)
	}
	if posted {
		return ErrResetPosted
	}
	if inFlight {
		return ErrResetInFlight
	}
	var putUp bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outreach_flyer_links WHERE outreach_id = $1 AND put_up_at IS NOT NULL)`, outreachID).Scan(&putUp); err != nil {
		return fmt.Errorf("outreach: reset check: %w", err)
	}
	if putUp {
		return ErrResetFlyerPutUp
	}
	return nil
}

// CanReset is true when nothing for the outreach has gone public. It is the
// rule behind the view's can_reset; Reset re-checks it under a lock.
func (s *Store) CanReset(ctx context.Context, outreachID int64) (bool, error) {
	err := resetBlock(ctx, s.pool, outreachID)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrResetPosted), errors.Is(err, ErrResetInFlight), errors.Is(err, ErrResetFlyerPutUp):
		return false, nil
	}
	return false, err
}

// Reset removes an outreach's posts (with their engagement and any finished
// queue rows) and flyers in one transaction, refusing with one of the
// ErrReset* errors if anything is public. The outreach row, its slot links
// (outreach_links) and its flyer links (outreach_flyer_links, with QR codes)
// are kept: ShortLinks has no delete, so keeping them means a later Draft
// reuses the same short links and creates no orphans.
func (s *Store) Reset(ctx context.Context, outreachID int64) (ResetResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Lock the outreach row first: FOR UPDATE conflicts with the FOR KEY SHARE
	// lock every insert into outreach_posts or outreach_flyers takes on its
	// parent row, so no post can be drafted (and then queued) after the check
	// below and be swept up by the deletes unseen.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM outreach WHERE id = $1 FOR UPDATE`, outreachID); err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset: %w", err)
	}
	// Serialise against the publisher's promoter, Post now, and flyer put-up.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM outreach_posts WHERE outreach_id = $1 FOR UPDATE`, outreachID); err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM outreach_flyer_links WHERE outreach_id = $1 FOR UPDATE`, outreachID); err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset: %w", err)
	}
	if err := resetBlock(ctx, tx, outreachID); err != nil {
		return ResetResult{}, err
	}
	var res ResetResult
	if _, err := tx.Exec(ctx, `
		DELETE FROM outbound_queue q USING outreach_posts p
		WHERE q.kind = $2 AND p.id = (q.payload->>'post_id')::bigint AND p.outreach_id = $1`,
		outreachID, string(outbox.KindOutreachPost)); err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset queue: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM outreach_posts WHERE outreach_id = $1`, outreachID)
	if err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset posts: %w", err)
	}
	res.Posts = int(tag.RowsAffected())
	tag, err = tx.Exec(ctx, `DELETE FROM outreach_flyers WHERE outreach_id = $1`, outreachID)
	if err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset flyers: %w", err)
	}
	res.Flyers = int(tag.RowsAffected())
	if err := tx.Commit(ctx); err != nil {
		return ResetResult{}, fmt.Errorf("outreach: reset: %w", err)
	}
	return res, nil
}
