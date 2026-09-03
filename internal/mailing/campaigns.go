// Campaign CRUD store (#0041, PRD §5.2/§6.6/§8) over the email_campaigns and
// campaign_interests tables migration 000017 (#0040) created. This file owns
// the campaign state machine's operator-facing transitions:
//
//	draft  -> scheduled   (Send:   the operator hits "send"/"schedule")
//	failed -> scheduled   (Send:   retry — see below)
//	scheduled, sending -> canceled (Cancel)
//
// It deliberately does NOT own scheduled -> sending or sending -> sent/failed
// — those are the send worker's claim/completion transitions (#0045), read
// fresh from the database at claim time, never driven by this API.
//
// # Why "failed -> scheduled", not "failed -> sending"
//
// #0045's plan (issues/0045.md §8, §13) notes that if #0041 ever offers a
// "retry a failed campaign" transition, the worker's drain resumes it safely
// and idempotently. Send accepts failed as a source status for exactly that
// reason: retrying re-enters the SAME "scheduled" queue a fresh campaign
// enters, so the worker's ordinary scheduled->sending claim (its own
// authority, not this package's) picks it up on its next poll. This package
// never writes 'sending' itself.
//
// # The empty-interest write-time guard (carried in from #0044's plan)
//
// normalizeAudience rejects an empty interest set for the any_of/all_of
// audience modes at every write (Create, Update, Send re-validates the
// stored row). #0044's own AudienceStore performs the identical check again
// at materialization time (its own ErrAudienceInterestsRequired) — this is
// deliberate belt-and-braces, not a shared implementation: catching it here
// tells the operator at compose time rather than at send time. See this
// package's ErrCampaignInterestsRequired doc comment for the reuse note.
//
// # The advisory Preflight seam (carried in from #0045's plan, 2026-08-21)
//
// mailing.Preflight (the pure pre-send gate function) does not exist yet —
// it is #0045's to build. Send calls an optional, narrowly-typed
// campaignPreflightChecker (internal/handlers/admin_campaigns.go) instead of
// depending on an undefined mailing type. See that file's doc comment for
// the full seam contract.
package mailing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Audience mode constants, verbatim from PRD §6.6's table and migration
// 000017's audience_mode CHECK. Declared here — the first file in this
// package to need them — so #0044's audience.go (not yet built) can import
// mailing.AudienceAll etc. rather than redeclare; see this issue's ## Fix.
const (
	AudienceAll          = "all"
	AudienceAnyOf        = "any_of"
	AudienceAllOf        = "all_of"
	AudienceNoneSelected = "none_selected"
)

// Campaign status constants, verbatim from migration 000017's status CHECK.
// CampaignStatusPausedDeliveryHealth was added by #0124 (PRD §6.9): the
// send worker's own mid-send circuit breaker, distinct from
// CampaignStatusFailed — a paused campaign is expected to be resumed
// (POST /admin/campaigns/{id}/resume), not retried from scratch.
const (
	CampaignStatusDraft                = "draft"
	CampaignStatusScheduled            = "scheduled"
	CampaignStatusSending              = "sending"
	CampaignStatusPausedDeliveryHealth = "paused_delivery_health"
	CampaignStatusSent                 = "sent"
	CampaignStatusCanceled             = "canceled"
	CampaignStatusFailed               = "failed"
)

// Archive status constants (#0123, migration 000025, PRD §6.8). A campaign
// is minted with 'pending' at Create time (the column default) and the
// worker's CompleteIfDone (worker_store.go) stamps 'published' +
// archived_at in the SAME UPDATE that flips status to 'sent' — so a
// campaign is never observably 'sent' with archive_status still 'pending'.
// 'withheld' is the admin's own lever (PATCH /admin/campaigns/{id}/archive,
// admin_campaign_archive.go): the page answers 410 Gone rather than 404,
// and is dropped from the public index and the sitemap, per PRD §6.8's
// table ("withheld" is a deliberate retraction, not "never existed").
const (
	ArchiveStatusPending   = "pending"
	ArchiveStatusPublished = "published"
	ArchiveStatusWithheld  = "withheld"
)

var (
	// ErrCampaignNotFound is returned when no email_campaigns row matches a
	// lookup. Declared here for #0044's LoadAudience to reuse rather than
	// redeclare (its plan names this exact error and message) — see ## Fix.
	ErrCampaignNotFound = errors.New("mailing: campaign not found")

	// ErrUnknownAudienceMode guards against a value outside the four PRD
	// §6.6 modes reaching a write. Defence in depth behind migration
	// 000017's CHECK constraint — mirrors #0044's planned
	// ErrUnknownAudienceMode; see ## Fix.
	ErrUnknownAudienceMode = errors.New("mailing: unknown audience mode")

	// ErrCampaignInterestsRequired is returned by Create/Update/Send when
	// audience_mode is any_of or all_of and no interest ids are attached.
	// Carried in from #0044's plan (2026-08-21): all_of over an empty array
	// evaluates 0 = 0 for every subscriber and silently means "the entire
	// list" — this must be impossible at every write, not just at
	// materialization. #0044's own materialize-time check
	// (ErrAudienceInterestsRequired) is a deliberate second, independent
	// enforcement of the same rule, not a shared implementation with this
	// one — see the package doc comment above.
	ErrCampaignInterestsRequired = errors.New("mailing: audience mode requires at least one interest")

	// ErrCampaignInterestNotFound is returned when one or more interest ids
	// in a Create/Update request do not exist (campaign_interests.interest_id's
	// FK, mapped from a 23503 foreign_key_violation to a typed error rather
	// than a raw constraint message).
	ErrCampaignInterestNotFound = errors.New("mailing: one or more interest ids do not exist")

	// ErrCampaignNotEditable is returned by Update when the campaign's
	// current status is not draft or scheduled (#0041's acceptance
	// criterion: "a campaign can only be edited while draft or scheduled").
	ErrCampaignNotEditable = errors.New("mailing: campaign can only be edited while draft or scheduled")

	// ErrIllegalStatusTransition is returned by Send/Cancel when the
	// campaign's current status is not a legal source for that transition.
	ErrIllegalStatusTransition = errors.New("mailing: illegal campaign status transition")

	// ErrCampaignScheduleTimeRequired is returned by Send when the resolved
	// scheduled_at would be nil. Send always supplies one (defaulting to
	// "now" when the caller doesn't specify a future time — see
	// internal/handlers/admin_campaigns.go), so this only ever fires as a
	// defensive check against a caller bypassing that default.
	ErrCampaignScheduleTimeRequired = errors.New("mailing: scheduled_at is required to schedule a campaign")

	// ErrCampaignSlugNotEditable is returned by Update when the caller
	// supplies a slug that differs from the campaign's current one while
	// the campaign's status is anything other than draft (#0123's
	// acceptance criterion: "editable while the campaign is draft;
	// immutable once the campaign leaves draft"). The slug is what a
	// short link or a piece of promotional copy is written against before
	// the send goes out (PRD §6.8's "load-bearing detail"), so it must not
	// be able to move out from under promotion that already points at it
	// once the campaign has scheduled or sent.
	ErrCampaignSlugNotEditable = errors.New("mailing: campaign slug can only be edited while draft")

	// ErrCampaignSlugTaken is returned by Update when a caller-supplied
	// slug collides with a different campaign's slug (email_campaigns'
	// UNIQUE(slug) constraint, migration 000025). Update does not run its
	// own collision-suffix retry the way Create does — Create's retry
	// exists because an operator never typed the candidate slug
	// themselves (it was derived from Subject), so silently trying "-2"
	// is a reasonable default; here the operator asked for THIS exact
	// slug, so the correct response is telling them it's taken, not
	// silently substituting a different one out from under a short link
	// they may already be about to write against it.
	ErrCampaignSlugTaken = errors.New("mailing: campaign slug is already in use")

	// errSlugAttemptsExhausted is wrapped into the error Create returns if
	// maxSlugAttempts consecutive candidates all collide against the
	// slug's UNIQUE constraint — effectively unreachable in practice, but
	// a bounded retry loop must still terminate somehow. Mirrors
	// workshops.errSlugAttemptsExhausted; see slugify's doc comment for
	// why the two packages each carry their own small copy rather than
	// sharing one.
	errSlugAttemptsExhausted = errors.New("mailing: could not generate a unique campaign slug")
)

// maxSlugAttempts bounds Create's collision-suffix retry loop, mirroring
// workshops.maxSlugAttempts.
const maxSlugAttempts = 1000

// campaignSlugNonAlnum matches any run of characters that are not a
// lowercase ASCII letter or digit, for collapsing into a single hyphen.
// A byte-for-byte copy of workshops.slugNonAlnum (internal/workshops/store.go)
// — the two packages don't share an internal/slug helper package because
// each is the only file in its own package that needs it, and PRD/CLAUDE.md
// carry no rule requiring code shared this thinly to be factored out.
var campaignSlugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugifyCampaign derives a URL slug from a campaign subject: lowercase,
// every run of non-alphanumeric characters collapsed to one hyphen,
// leading/trailing hyphens trimmed. An input that slugifies to "" (a
// subject that is all punctuation/emoji) falls back to "campaign" so
// Create's INSERT never attempts an empty slug. Mirrors
// workshops.slugify exactly, including the fallback shape.
func slugifyCampaign(subject string) string {
	lower := strings.ToLower(subject)
	hyphenated := campaignSlugNonAlnum.ReplaceAllString(lower, "-")
	trimmed := strings.Trim(hyphenated, "-")
	if trimmed == "" {
		return "campaign"
	}
	return trimmed
}

// Campaign is a single email_campaigns row plus its campaign_interests
// targeting, loaded as a side query by GetByID/List/Create/Update/Send/Cancel
// (never part of the base SELECT — see interestIDs).
type Campaign struct {
	ID           int64
	Name         string
	Subject      string
	Preheader    *string
	BodyMD       string
	Status       string
	AudienceMode string
	WorkshopID   *int64
	InterestIDs  []int64
	ScheduledAt  *time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	// Slug is email_campaigns.slug (migration 000025, #0123, PRD §6.8) —
	// minted at Create time from Subject (slugifyCampaign), never blank.
	// It is the permanent public identity of this campaign's archive page
	// (/archive/{slug}) and is generated up front, at draft time, so
	// promotion (a go.opencircuitsf.com short link, a Discord post) can be
	// written against the final URL before the send goes out — see this
	// package's slugifyCampaign doc comment and Create's own comment for
	// the collision-retry algorithm. Editable only while Status is draft
	// (Update returns ErrCampaignSlugNotEditable otherwise).
	Slug string
	// ArchiveStatus is email_campaigns.archive_status: 'pending' (not yet
	// sent), 'published' (sent — stamped atomically by the worker's
	// CompleteIfDone alongside Status -> 'sent'), or 'withheld' (an admin
	// deliberately pulled it — PATCH /admin/campaigns/{id}/archive). See
	// the ArchiveStatus* constants above.
	ArchiveStatus string
	// ArchivedAt is email_campaigns.archived_at — nil until the campaign's
	// ArchiveStatus first becomes 'published', then permanent (never
	// cleared by a later withhold — a 'withheld' page's ArchivedAt still
	// records when it first went live).
	ArchivedAt *time.Time
	// TestSentAt is email_campaigns.test_sent_at (migration 000018). nil
	// means no test send has ever been delivered. #0046's
	// AdminCampaignPreviewHandler.Test is the sole writer (MarkTestSent
	// below); #0045's SendStore.GatherPreflight reads it directly with its
	// own query rather than through scanCampaign, so it stays the single
	// place both preflight call sites gather this fact from. Exposed here,
	// on the Campaign struct itself, purely for the admin API's own
	// visibility (campaignView in internal/handlers) — Update's own
	// clearing logic (below) already writes this column without needing to
	// read it back through this struct.
	TestSentAt *time.Time
	CreatedBy  *int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CampaignInput is the payload for Create.
//
// WorkshopID is optional provenance (nil for an ordinary hand-composed
// campaign): #0056's announce-to-list shortcut is the first and, as of this
// writing, only caller that sets it, linking the created campaign back to
// the workshop it was pre-filled from via email_campaigns.workshop_id
// (migrations 000017/000020). That FK carries no ON DELETE clause by design
// — workshops.Store.Delete refuses (ErrHasCampaigns) rather than orphan or
// cascade this link, see that package's doc comment.
type CampaignInput struct {
	Name         string
	Subject      string
	Preheader    *string
	BodyMD       string
	AudienceMode string
	InterestIDs  []int64
	WorkshopID   *int64
	CreatedBy    *int64
	// NewsletterMonth opts this campaign into #0405's MM-YYYY archive-slug
	// template (issues/0405.md — the user's own decision, "It can be short
	// and specific"). nil (the default, and the ONLY value the announce
	// shortcut ever sets — admin_workshop_announce.go never touches this
	// field) leaves slug minting exactly as it was before #0405:
	// slugifyCampaign(Subject). Opt-in only, never inferred — #0405's plan
	// measured all three plausible inference rules (workshop_id IS NULL,
	// audience_mode == "all", subject/name text matching) failing against
	// the two real CampaignInput{} call sites, so nothing here derives this
	// from any other field. See newsletter_month.go for NewsletterMonth
	// itself.
	NewsletterMonth *NewsletterMonth
}

// CampaignUpdate is the payload for Update. Unlike a partial PATCH body, this
// carries every field's fully-resolved target value — the handler merges the
// caller's optional patch fields onto the current row (mirroring
// AdminInterestsHandler.Patch's own merge-then-call-Update shape) before
// calling this method, so the store never has to reason about "was this
// field provided".
type CampaignUpdate struct {
	Name         string
	Subject      string
	Preheader    *string
	BodyMD       string
	AudienceMode string
	InterestIDs  []int64
	// Slug is the target slug. Callers that don't intend to change it pass
	// back the campaign's current value (mirroring every other field's
	// "fully-resolved target value" contract above) — Update only rejects
	// (ErrCampaignSlugNotEditable) when this differs from the stored slug
	// AND the campaign's current status is not draft; passing the
	// unchanged slug is always accepted regardless of status, so a caller
	// that merges-then-calls-Update without touching the slug field never
	// has to special-case it.
	Slug string
}

// CampaignStore is the data-access layer over email_campaigns and
// campaign_interests.
type CampaignStore struct {
	pool *pgxpool.Pool
}

// NewCampaignStore constructs a CampaignStore over the shared connection pool.
func NewCampaignStore(pool *pgxpool.Pool) *CampaignStore {
	return &CampaignStore{pool: pool}
}

const campaignColumns = `id, name, subject, preheader, body_md, status, audience_mode,
	workshop_id, scheduled_at, started_at, completed_at, test_sent_at, created_by, created_at, updated_at,
	slug, archive_status, archived_at`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	if err := row.Scan(
		&c.ID, &c.Name, &c.Subject, &c.Preheader, &c.BodyMD, &c.Status, &c.AudienceMode,
		&c.WorkshopID, &c.ScheduledAt, &c.StartedAt, &c.CompletedAt, &c.TestSentAt, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt,
		&c.Slug, &c.ArchiveStatus, &c.ArchivedAt,
	); err != nil {
		return Campaign{}, err
	}
	return c, nil
}

// normalizeAudience validates mode against the closed vocabulary and enforces
// the carried-in "empty interest set" guard for any_of/all_of. Called by
// every write path (Create, Update, and Send's defensive re-check).
func normalizeAudience(mode string, interestIDs []int64) error {
	switch mode {
	case AudienceAll, AudienceAnyOf, AudienceAllOf, AudienceNoneSelected:
	default:
		return ErrUnknownAudienceMode
	}
	if (mode == AudienceAnyOf || mode == AudienceAllOf) && len(interestIDs) == 0 {
		return ErrCampaignInterestsRequired
	}
	return nil
}

// dedupeInt64 returns ids with duplicates removed, preserving first
// occurrence order. Create/Update accept a client-supplied interest id list;
// deduplicating here means a client that double-submits (or a checkbox UI
// state bug) never trips campaign_interests' (campaign_id, interest_id)
// primary key as a raw, unhelpful constraint-violation 500.
func dedupeInt64(ids []int64) []int64 {
	if len(ids) == 0 {
		return ids
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// isForeignKeyViolation reports whether err is a Postgres foreign_key_violation
// (SQLSTATE 23503), e.g. an interest id that does not exist.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// isUniqueViolation reports whether err is a Postgres unique_violation
// (SQLSTATE 23505) — used by Create's slug-collision retry loop. A copy of
// workshops.isUniqueViolation (internal/workshops/store.go), which serves
// the identical purpose for that package's own slug retry; see
// slugifyCampaign's doc comment for why this package carries its own small
// copy rather than a shared helper package.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// replaceInterestsTx replaces a campaign's full campaign_interests set with
// interestIDs (delete-then-insert, in the caller's transaction) — the same
// shape internal/subscribers.Store.SetInterests uses for subscriber_interests,
// so a partial write can never leave a stale association behind.
func (s *CampaignStore) replaceInterestsTx(ctx context.Context, tx pgx.Tx, campaignID int64, interestIDs []int64) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM campaign_interests WHERE campaign_id = $1`, campaignID,
	); err != nil {
		return fmt.Errorf("mailing: clearing campaign interests for %d: %w", campaignID, err)
	}
	for _, interestID := range dedupeInt64(interestIDs) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO campaign_interests (campaign_id, interest_id) VALUES ($1, $2)`,
			campaignID, interestID,
		); err != nil {
			if isForeignKeyViolation(err) {
				return ErrCampaignInterestNotFound
			}
			return fmt.Errorf("mailing: linking interest %d to campaign %d: %w", interestID, campaignID, err)
		}
	}
	return nil
}

// interestIDs returns the sorted interest ids attached to a campaign.
func (s *CampaignStore) interestIDs(ctx context.Context, campaignID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT interest_id FROM campaign_interests WHERE campaign_id = $1 ORDER BY interest_id`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("mailing: loading interests for campaign %d: %w", campaignID, err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("mailing: scanning campaign interest row: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mailing: iterating campaign interest rows: %w", err)
	}
	return ids, nil
}

// interestIDsByCampaign batch-loads interest ids for every campaign id in
// ids, for List (avoids N+1 queries for an N-row campaign list).
func (s *CampaignStore) interestIDsByCampaign(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT campaign_id, interest_id FROM campaign_interests
		  WHERE campaign_id = ANY($1)
		  ORDER BY campaign_id, interest_id`, ids)
	if err != nil {
		return nil, fmt.Errorf("mailing: batch loading campaign interests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var campaignID, interestID int64
		if err := rows.Scan(&campaignID, &interestID); err != nil {
			return nil, fmt.Errorf("mailing: scanning campaign interest row: %w", err)
		}
		out[campaignID] = append(out[campaignID], interestID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mailing: iterating campaign interest rows: %w", err)
	}
	return out, nil
}

// List returns every campaign, newest first, each carrying its InterestIDs.
// No pagination — #0041's acceptance criteria do not call for it, and a
// mailing-list operator's campaign count is small (unlike the subscriber
// list, which does paginate).
func (s *CampaignStore) List(ctx context.Context) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+campaignColumns+` FROM email_campaigns ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("mailing: listing campaigns: %w", err)
	}
	defer rows.Close()
	var campaigns []Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, fmt.Errorf("mailing: scanning campaign row: %w", err)
		}
		campaigns = append(campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mailing: iterating campaign rows: %w", err)
	}

	ids := make([]int64, len(campaigns))
	for i, c := range campaigns {
		ids[i] = c.ID
	}
	interestMap, err := s.interestIDsByCampaign(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range campaigns {
		campaigns[i].InterestIDs = interestMap[campaigns[i].ID]
	}
	return campaigns, nil
}

// GetByID loads a single campaign with its InterestIDs. Returns
// ErrCampaignNotFound when no row matches.
func (s *CampaignStore) GetByID(ctx context.Context, id int64) (Campaign, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM email_campaigns WHERE id = $1`, id)
	c, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Campaign{}, ErrCampaignNotFound
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: getting campaign %d: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	c.InterestIDs = ids
	return c, nil
}

// Create inserts a new campaign in status='draft' plus its campaign_interests
// rows, atomically. Rejects an unknown audience_mode or an empty interest set
// for any_of/all_of before any write (normalizeAudience).
//
// # Slug minting (#0123, PRD §6.8)
//
// A slug is generated from in.Subject (slugifyCampaign) and uniquified on
// collision with a numeric suffix ("-2", "-3", ...) exactly the way
// workshops.Store.Create mints a workshop's slug — see that method's own
// doc comment for the full reasoning, mirrored here: each candidate is a
// real INSERT fought out against email_campaigns' UNIQUE(slug) constraint
// inside its own pgx pseudo-nested transaction (SAVEPOINT / RELEASE
// SAVEPOINT / ROLLBACK TO SAVEPOINT), never a pre-check-then-insert race,
// so two concurrent Creates with the same subject can never both win the
// same slug. The slug is minted here, at draft time, not at send time —
// PRD §6.8's "load-bearing detail": promotion for a campaign has to be
// written against a URL that already exists.
func (s *CampaignStore) Create(ctx context.Context, in CampaignInput) (Campaign, error) {
	if err := normalizeAudience(in.AudienceMode, in.InterestIDs); err != nil {
		return Campaign{}, err
	}

	base := slugifyCampaign(in.Subject)
	if in.NewsletterMonth != nil {
		if !in.NewsletterMonth.valid() {
			return Campaign{}, ErrInvalidNewsletterMonth
		}
		base = in.NewsletterMonth.Slug()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Campaign{}, fmt.Errorf("mailing: beginning create-campaign tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var c Campaign
	var insertErr error
	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return Campaign{}, fmt.Errorf("mailing: starting slug-attempt savepoint for %q: %w", in.Name, err)
		}
		row := sp.QueryRow(ctx,
			`INSERT INTO email_campaigns (name, subject, preheader, body_md, audience_mode, workshop_id, created_by, slug)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 RETURNING `+campaignColumns,
			in.Name, in.Subject, in.Preheader, in.BodyMD, in.AudienceMode, in.WorkshopID, in.CreatedBy, candidate,
		)
		c, insertErr = scanCampaign(row)
		if insertErr == nil {
			if err := sp.Commit(ctx); err != nil {
				return Campaign{}, fmt.Errorf("mailing: releasing slug-attempt savepoint for %q: %w", in.Name, err)
			}
			break
		}
		if !isUniqueViolation(insertErr) {
			return Campaign{}, fmt.Errorf("mailing: creating campaign %q: %w", in.Name, insertErr)
		}
		if err := sp.Rollback(ctx); err != nil {
			return Campaign{}, fmt.Errorf("mailing: rolling back slug-attempt savepoint for %q: %w", in.Name, err)
		}
	}
	if insertErr != nil {
		return Campaign{}, fmt.Errorf("mailing: creating campaign %q: %w", in.Name, errSlugAttemptsExhausted)
	}

	interestIDs := dedupeInt64(in.InterestIDs)
	if err := s.replaceInterestsTx(ctx, tx, c.ID, interestIDs); err != nil {
		return Campaign{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Campaign{}, fmt.Errorf("mailing: committing create-campaign tx: %w", err)
	}
	c.InterestIDs = interestIDs
	return c, nil
}

// Update replaces a campaign's editable content fields and targeting.
// Content-only — it never changes status; see Send/Cancel for the two
// operator-triggered status transitions this package owns. Refuses
// (ErrCampaignNotEditable) unless the campaign's CURRENT status, read inside
// this method's own transaction under FOR UPDATE, is draft or scheduled —
// re-checked here rather than trusted from an earlier read, so a concurrent
// transition (a second admin's edit, or #0045's worker claiming the campaign)
// can't race a stale "it was draft when I loaded it" decision.
//
// # test_sent_at is cleared on a content edit (landed by #0045, migration 000018)
//
// #0045's plan (issues/0045.md §13) requires that editing subject or body_md
// clear email_campaigns.test_sent_at to NULL, so a content edit after a test
// send correctly re-arms the "no_test_send" preflight failure — otherwise an
// operator could send a materially different body than the one #0046's test
// send actually delivered and reviewed. Preheader is deliberately NOT part
// of this comparison: it never reaches #0046's rendered preview body (only
// the HTML document's hidden preview-text div), so editing it alone would
// re-arm a gate over a change the test send never exercised. The comparison
// is IS DISTINCT FROM, not <>, so a NULL-to-NULL "change" (impossible for
// these NOT NULL columns, but a safe default) never misfires.
func (s *CampaignStore) Update(ctx context.Context, id int64, in CampaignUpdate) (Campaign, error) {
	if err := normalizeAudience(in.AudienceMode, in.InterestIDs); err != nil {
		return Campaign{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Campaign{}, fmt.Errorf("mailing: beginning update-campaign tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	lockRow := tx.QueryRow(ctx,
		`SELECT `+campaignColumns+` FROM email_campaigns WHERE id = $1 FOR UPDATE`, id)
	current, err := scanCampaign(lockRow)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Campaign{}, ErrCampaignNotFound
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: locking campaign %d: %w", id, err)
	}

	if current.Status != CampaignStatusDraft && current.Status != CampaignStatusScheduled {
		return Campaign{}, ErrCampaignNotEditable
	}

	// Slug: immutable once the campaign leaves draft (#0123's acceptance
	// criterion — see ErrCampaignSlugNotEditable's doc comment for why).
	// Passing back the unchanged slug is always accepted, so a caller that
	// merges-then-calls-Update without touching this field never trips the
	// check; a genuinely different slug is refused unless status is still
	// draft.
	//
	// An empty in.Slug is treated as "keep current" rather than as a value
	// to write (review of #0123, 2026-08-27): AdminCampaignsHandler.Patch
	// once built CampaignUpdate without a Slug field at all, so every PATCH
	// carried the zero value and blanked the column outright, tripping
	// email_campaigns' UNIQUE(slug) on the second draft edited and hitting
	// ErrCampaignSlugNotEditable (unmapped to any HTTP status, so a 500) on
	// any scheduled campaign's edit. The handler is fixed to pass the
	// current value through explicitly, but this guard makes the same
	// omission by any future caller harmless rather than destructive.
	slug := in.Slug
	if slug == "" {
		slug = current.Slug
	}
	if slug != current.Slug && current.Status != CampaignStatusDraft {
		return Campaign{}, ErrCampaignSlugNotEditable
	}

	// See the "test_sent_at" doc comment above: a subject or body_md change
	// clears test_sent_at back to NULL so the no_test_send gate re-arms.
	row := tx.QueryRow(ctx,
		`UPDATE email_campaigns
		    SET name = $2, subject = $3, preheader = $4, body_md = $5,
		        audience_mode = $6, slug = $7, updated_at = now(),
		        test_sent_at = CASE
		            WHEN subject IS DISTINCT FROM $3 OR body_md IS DISTINCT FROM $5
		            THEN NULL ELSE test_sent_at END
		  WHERE id = $1
		  RETURNING `+campaignColumns,
		id, in.Name, in.Subject, in.Preheader, in.BodyMD, in.AudienceMode, slug,
	)
	updated, err := scanCampaign(row)
	switch {
	case err == nil:
	case isUniqueViolation(err):
		return Campaign{}, ErrCampaignSlugTaken
	default:
		return Campaign{}, fmt.Errorf("mailing: updating campaign %d: %w", id, err)
	}

	interestIDs := dedupeInt64(in.InterestIDs)
	if err := s.replaceInterestsTx(ctx, tx, id, interestIDs); err != nil {
		return Campaign{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Campaign{}, fmt.Errorf("mailing: committing update-campaign tx: %w", err)
	}
	updated.InterestIDs = interestIDs
	return updated, nil
}

// Send transitions a campaign from draft OR failed into scheduled — the
// operator-initiated action that enters the send pipeline (PRD §8's
// POST /admin/campaigns/{id}/send). It is deliberately the only place this
// package writes 'scheduled'.
//
// failed is accepted as a source status, alongside draft, as a retry path:
// #0045's plan (issues/0045.md §8/§13) confirms the worker's drain is
// idempotent and resumes a re-queued campaign safely, so re-entering
// 'scheduled' from 'failed' is safe. This package never writes 'sending'
// directly — the worker's own scheduled->sending claim (#0045) is what picks
// up either a fresh or a retried campaign.
//
// scheduledAt must be non-nil (ErrCampaignScheduleTimeRequired) — callers
// that want "send as soon as possible" pass time.Now(), not nil; a nil value
// here would produce a scheduled_at the worker's `scheduled_at <= now()`
// claim predicate can never satisfy, silently stranding the campaign.
//
// Re-validates normalizeAudience against the STORED row (defence in depth:
// Create/Update already enforce this at every write, but re-checking here
// means a row that somehow reached an invalid state — a direct SQL edit, a
// future migration bug — is refused at send rather than silently mailing the
// wrong audience).
func (s *CampaignStore) Send(ctx context.Context, id int64, scheduledAt time.Time) (Campaign, error) {
	if scheduledAt.IsZero() {
		return Campaign{}, ErrCampaignScheduleTimeRequired
	}

	current, err := s.GetByID(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if current.Status != CampaignStatusDraft && current.Status != CampaignStatusFailed {
		return Campaign{}, ErrIllegalStatusTransition
	}
	if err := normalizeAudience(current.AudienceMode, current.InterestIDs); err != nil {
		return Campaign{}, err
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE email_campaigns
		    SET status = $2, scheduled_at = $3, updated_at = now()
		  WHERE id = $1 AND status IN ($4, $5)
		  RETURNING `+campaignColumns,
		id, CampaignStatusScheduled, scheduledAt, CampaignStatusDraft, CampaignStatusFailed,
	)
	updated, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The pre-check above already confirmed the row exists and was
		// draft/failed; a NoRows here means it transitioned out from under
		// us between that read and this UPDATE (a concurrent Send/Cancel, or
		// the worker) — report the same illegal-transition error rather than
		// a confusing not-found.
		return Campaign{}, ErrIllegalStatusTransition
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: sending campaign %d: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	updated.InterestIDs = ids
	return updated, nil
}

// Cancel transitions a scheduled or sending campaign to canceled. It never
// recalls already-sent messages (PRD §8's note, restated in #0041's Notes) —
// cancelling a 'sending' campaign only stops #0045's worker from claiming
// further queued rows on its next poll; it cannot undo a send already in
// flight or already committed.
func (s *CampaignStore) Cancel(ctx context.Context, id int64) (Campaign, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE email_campaigns
		    SET status = $2, updated_at = now()
		  WHERE id = $1 AND status IN ($3, $4, $5)
		  RETURNING `+campaignColumns,
		// #0124: a campaign paused by the delivery-health circuit breaker
		// is also cancellable directly — an operator who has diagnosed the
		// underlying problem may decide the campaign should not resume at
		// all, and forcing a Resume-then-Cancel round trip through the
		// worker would serve no purpose.
		id, CampaignStatusCanceled, CampaignStatusScheduled, CampaignStatusSending, CampaignStatusPausedDeliveryHealth,
	)
	updated, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Distinguish "no such campaign" (404) from "exists but not
		// cancellable" (409) — the UPDATE alone can't tell them apart.
		switch _, gerr := s.GetByID(ctx, id); {
		case errors.Is(gerr, ErrCampaignNotFound):
			return Campaign{}, ErrCampaignNotFound
		case gerr != nil:
			return Campaign{}, gerr
		default:
			return Campaign{}, ErrIllegalStatusTransition
		}
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: cancelling campaign %d: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	updated.InterestIDs = ids
	return updated, nil
}

// Resume transitions a paused_delivery_health campaign back to scheduled
// (#0124, PRD §6.9) — the same "hand it back to the worker's ordinary
// scheduled -> sending claim" shape Send uses for a failed-campaign retry,
// not a dedicated resume-specific worker path. scheduledAt is "now" for an
// immediate resume; a caller wanting a delay can pass a future time, though
// internal/handlers/admin_campaigns.go's Resume handler always passes now().
//
// The ONLY legal source status is paused_delivery_health — deliberately
// narrower than Send's draft-or-failed set, since Resume exists to answer
// one specific question ("this campaign was paused by the circuit breaker;
// let it continue") and answering it for a draft or a failed campaign would
// just be Send under a different name and a different (weaker) confirmation
// shape. drainLoop re-evaluates the circuit breaker fresh on the next pass —
// resuming does not reset or bypass it (this issue's "not bypassable from
// the UI" criterion): a campaign whose rate is still over threshold once
// enough NEW sends accumulate trips again.
func (s *CampaignStore) Resume(ctx context.Context, id int64, scheduledAt time.Time) (Campaign, error) {
	if scheduledAt.IsZero() {
		return Campaign{}, ErrCampaignScheduleTimeRequired
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE email_campaigns
		    SET status = $2, scheduled_at = $3, updated_at = now()
		  WHERE id = $1 AND status = $4
		  RETURNING `+campaignColumns,
		id, CampaignStatusScheduled, scheduledAt, CampaignStatusPausedDeliveryHealth,
	)
	updated, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		switch _, gerr := s.GetByID(ctx, id); {
		case errors.Is(gerr, ErrCampaignNotFound):
			return Campaign{}, ErrCampaignNotFound
		case gerr != nil:
			return Campaign{}, gerr
		default:
			return Campaign{}, ErrIllegalStatusTransition
		}
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: resuming campaign %d: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	updated.InterestIDs = ids
	return updated, nil
}

// MarkTestSent stamps email_campaigns.test_sent_at = at, after a real test
// send has actually been delivered (#0046,
// internal/handlers/admin_campaign_preview.go's Test). This is the ONLY
// write path for this column — #0045's send worker (worker_store.go's
// GatherPreflight) only ever reads it, gating the no_test_send preflight
// failure on it being non-nil. See CampaignUpdate's clearing logic (above,
// in Update) for the only other place this column changes: a subsequent
// subject/body_md edit clears it back to NULL so the gate re-arms.
//
// Deliberately does NOT require any particular status and does NOT bump
// updated_at: a test send is not a content edit, and #0041's review already
// established that test_sent_at must be compared for nilness only, never
// against updated_at (a bare status bump would otherwise deadlock the
// operator into re-testing forever — see worker_store.go's package doc
// comment). An operator may re-verify a campaign's rendering at any time,
// including after it has already sent, so this is never refused on status.
func (s *CampaignStore) MarkTestSent(ctx context.Context, id int64, at time.Time) (Campaign, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE email_campaigns SET test_sent_at = $2
		  WHERE id = $1
		  RETURNING `+campaignColumns,
		id, at,
	)
	updated, err := scanCampaign(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Campaign{}, ErrCampaignNotFound
	case err != nil:
		return Campaign{}, fmt.Errorf("mailing: marking campaign %d test-sent: %w", id, err)
	}
	ids, err := s.interestIDs(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	updated.InterestIDs = ids
	return updated, nil
}
