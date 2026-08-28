// imports.go adds subscriber_imports (migrations/000023, PRD §6.10) as a
// third table this package owns, alongside subscribers/subscriber_interests/
// suppressions/subscriber_events — the admin-only CSV import path (#0125)
// for audiences already collected elsewhere (a Google Form, an event
// sign-in sheet), with consent provenance recorded up front and a batch
// revocable in one action.
//
// # Two consent modes, both implemented here
//
// subscriber_imports.consent_mode is either prior_consent (#0125: lands the
// address `active`, sends nothing — the admin is attesting consent already
// exists) or invite (#0129: lands the address `pending` with a confirm
// token, sends exactly one invitation through internal/outbox naming where
// the address came from, and only moves it to `active` when the recipient
// follows the SAME confirm link the public double opt-in flow uses —
// internal/subscribers.Store.Confirm, not a second confirmation path).
// consent_basis stays NULL until that confirmation happens; #0125's Commit
// refused ConsentModeInvite outright (ErrConsentModeNotSupported) precisely
// so the wizard could not silently downgrade an invite request to
// prior_consent before this issue existed to implement it — see
// issues/0125.md's acceptance criteria and issues/0129.md for the mode this
// file now also commits.
//
// # Preview does not write; Commit re-derives its own dedupe/suppression sets
//
// ImportStore.Preview runs the same classification Commit will (new /
// duplicate / suppressed), entirely read-only, so the wizard can show counts
// before committing. Commit does NOT trust Preview's counts — a second
// request could arrive with a changed subscribers/suppressions table in
// between (another admin's action, a bounce arriving via SES) — it
// re-queries both sets INSIDE its own transaction, immediately before
// inserting, so the classification that actually governs what gets written
// is never more than one query old. The caller (internal/handlers/
// admin_subscribers_import.go) is responsible for the checksum tying a
// commit request back to the exact file bytes a preview was run against —
// this package has no notion of "the same upload", only "the same
// candidate list".
//
// # Suppression and dedupe are absolute
//
// Every candidate address is checked against BOTH subscribers (any status —
// an import never overwrites an existing row, regardless of what state it's
// in) and suppressions (any reason) before insert. This is not a
// convenience filter; it is the mechanism that keeps an import from being
// the tool that resurrects a hard-bounced or complained address (CLAUDE.md
// §9, #0033/#0038/#0100).
package subscribers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// Import source values (subscriber_imports.source — where the BATCH says it
// came from), matching subscriber_imports_source_check. Distinct from the
// Subscriber* source constants in store.go, which describe how an
// individual subscribers row entered the table (always SubscriberSourceImport
// for a row this package inserts).
const (
	ImportSourceLuma       = "luma"
	ImportSourceEventbrite = "eventbrite"
	ImportSourceMeetup     = "meetup"
	ImportSourceManualCSV  = "manual_csv"
	ImportSourceOther      = "other"
)

var importSources = map[string]bool{
	ImportSourceLuma:       true,
	ImportSourceEventbrite: true,
	ImportSourceMeetup:     true,
	ImportSourceManualCSV:  true,
	ImportSourceOther:      true,
}

// Consent modes, matching subscriber_imports_consent_mode_check. Both have
// a Commit implementation — see the package doc comment.
const (
	ConsentModePriorConsent = "prior_consent"
	ConsentModeInvite       = "invite"
)

var consentModes = map[string]bool{
	ConsentModePriorConsent: true,
	ConsentModeInvite:       true,
}

// Import statuses, matching subscriber_imports_status_check.
const (
	ImportStatusCommitted = "committed"
	ImportStatusRevoked   = "revoked"
)

// Errors returned by Commit's validation, before any database write.
var (
	ErrInvalidImportSource = errors.New("subscribers: invalid import source")
	ErrInvalidConsentMode  = errors.New("subscribers: invalid consent mode")
	// ErrSourceDetailRequired is returned by Commit for a blank source_detail
	// (#0291, PRD §6.10: "source_detail — the specific event or export it
	// came from" is one of four fields a subscriber_imports row requires).
	// Enforced here in addition to migrations/000024's NOT NULL/CHECK, the
	// same belt-and-suspenders pattern ErrConsentNoteRequired already
	// follows — the database constraint is the backstop, this is what lets
	// Commit return a field-naming error instead of a raw constraint
	// violation.
	ErrSourceDetailRequired = errors.New("subscribers: source_detail is required")
	ErrConsentNoteRequired  = errors.New("subscribers: consent_note is required")
	ErrCollectedAtRequired  = errors.New("subscribers: collected_at is required")
)

// importInviteConfirmTTL is the invitation's confirm-token lifetime — #0129
// names no different figure than the public double opt-in flow's own
// (internal/handlers/subscribe.go's subscribeConfirmTTL, 7 days), and an
// invited address should not face a shorter or longer window than a website
// signup for the identical action (following the SAME confirm link,
// internal/subscribers.Store.Confirm). Declared independently rather than
// imported — internal/handlers depends on this package, not the reverse,
// and this package "imports nothing internal" beyond internal/outbox (see
// store.go's package doc comment) — so this is a deliberate, disclosed
// duplication of one constant's VALUE, not its identity.
const importInviteConfirmTTL = 7 * 24 * time.Hour

// importInvitePayload is outbound_queue.payload's shape for
// outbox.KindImportInvite — template inputs, matched by JSON field name
// with internal/mailing's own importInvitePayload (different Go type, same
// package boundary internal/outbox's doc comment describes for every other
// producer/consumer pair in this project). ImportSource/SourceDetail/
// CollectedAt are captured HERE, from the subscriber_imports row THIS
// Commit call just created, not re-read by the worker at send time — the
// same "captured, not re-derived" convention store.go's welcomePayload
// follows for InterestNames, so a later edit to the import row (there is no
// such edit path today, but the convention is the same regardless) can
// never retroactively rewrite what an already-queued invitation says.
type importInvitePayload struct {
	ConfirmToken string    `json:"confirm_token"`
	ManageToken  string    `json:"manage_token"`
	TTLSeconds   int64     `json:"ttl_seconds"`
	ImportSource string    `json:"import_source"`
	SourceDetail string    `json:"source_detail"`
	CollectedAt  time.Time `json:"collected_at"`
}

// ErrImportNotFound is returned by GetImport/Revoke when no subscriber_imports
// row matches the given id.
var ErrImportNotFound = errors.New("subscribers: import not found")

// ErrRevokeReasonRequired is returned by Revoke when reason is empty after
// trimming — matching ConsentNote's own "the admin's words, required"
// convention: undoing a batch is exactly the kind of action that needs a
// reason on record.
var ErrRevokeReasonRequired = errors.New("subscribers: revoke reason is required")

// Import is one subscriber_imports row.
type Import struct {
	ID             int64
	Source         string
	SourceDetail   *string
	ConsentMode    string
	ConsentNote    string
	CollectedAt    time.Time
	Filename       *string
	RowCount       int
	InsertedCount  int
	SkippedCount   int
	InvitedCount   int
	ConfirmedCount int
	Status         string
	RevokedAt      *time.Time
	RevokedReason  *string
	ImportedBy     *int64
	CreatedAt      time.Time
}

// ImportRow is one CSV data row, already column-mapped by the caller
// (internal/handlers/admin_subscribers_import.go parses the upload and
// picks the email/interest columns — "the email column is never guessed
// from the header", per #0125's acceptance criteria). Email is expected to
// already have passed syntax validation; a row that failed it is a
// "malformed" row the caller counts separately and never passes in here —
// this package imports nothing internal (see internal/outbox's doc comment
// for why that invariant matters) and so cannot call the handlers-package
// email-syntax validator itself.
type ImportRow struct {
	Email         string
	InterestSlugs []string // raw, lowercased/trimmed by the caller; may include unknown slugs
}

// importSampleSize bounds how many example addresses Preview returns per
// bucket — enough for an admin to sanity-check the classification without
// the response ballooning to the size of the whole file.
const importSampleSize = 10

// PreviewResult is the read-only classification #0125's wizard shows before
// a commit. NewCount+DuplicateCount+SuppressedCount always sums to the
// number of DISTINCT (post-normalization) candidate emails handed in — a
// within-file repeat of the same address is folded into whichever bucket its
// first occurrence landed in, never double-counted.
type PreviewResult struct {
	NewCount         int
	DuplicateCount   int
	SuppressedCount  int
	SampleNew        []string
	SampleDuplicate  []string
	SampleSuppressed []string
	// UnknownInterestSlugs are slugs the interest column named that do not
	// match any known interest, sorted, deduplicated — reported rather than
	// silently dropped, per #0125's acceptance criteria.
	UnknownInterestSlugs []string
}

// dedupeCandidates returns rows' emails, distinct by lower(trim(...)), in
// first-seen order, alongside the original (non-lowered) string to insert.
// Matching normalization is done in Go here ONLY for grouping candidates
// into a request array; the database's own lower(trim(...)) is still what
// actually decides equality against subscribers/suppressions (see
// existingEmails/suppressedEmails below), so this cannot disagree with the
// CHECK constraint the way store.go's package doc comment warns
// strings.ToLower can on rare Unicode input — a false split here only ever
// costs an extra (harmless) row in the candidate list, never a wrong
// new/duplicate/suppressed classification.
func dedupeCandidates(rows []ImportRow) []string {
	seen := make(map[string]bool, len(rows))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		key := strings.ToLower(strings.TrimSpace(r.Email))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r.Email)
	}
	return out
}

// querier is reused from suppressions.go — Exec/QueryRow/Query, satisfied
// by *pgxpool.Pool and pgx.Tx.

// existingEmails returns, of candidates, the subset already present in
// subscribers — ANY status, since an import must skip an existing row
// regardless of what state it is in (never overwritten, per #0125's
// acceptance criteria). The unnest/JOIN shape normalizes candidates with
// the SAME lower(trim(...)) expression subscribers.email is stored under,
// so this can never disagree with the table's own
// subscribers_email_normalized CHECK.
func existingEmails(ctx context.Context, q querier, candidates []string) (map[string]bool, error) {
	return normalizedEmailSet(ctx, q, `
		SELECT DISTINCT s.email
		  FROM subscribers s
		  JOIN (SELECT DISTINCT lower(trim(x)) AS email FROM unnest($1::text[]) AS x) cand
		    ON cand.email = s.email`, candidates)
}

// suppressedEmails is existingEmails' twin over suppressions — any reason
// blocks (matching SuppressionStore.IsSuppressed's own reason-blind
// contract).
func suppressedEmails(ctx context.Context, q querier, candidates []string) (map[string]bool, error) {
	return normalizedEmailSet(ctx, q, `
		SELECT DISTINCT sup.email
		  FROM suppressions sup
		  JOIN (SELECT DISTINCT lower(trim(x)) AS email FROM unnest($1::text[]) AS x) cand
		    ON cand.email = sup.email`, candidates)
}

func normalizedEmailSet(ctx context.Context, q querier, query string, candidates []string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(candidates) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, query, candidates)
	if err != nil {
		return nil, fmt.Errorf("subscribers: matching candidate emails: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, fmt.Errorf("subscribers: scanning matched email: %w", err)
		}
		out[email] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("subscribers: iterating matched emails: %w", err)
	}
	return out, nil
}

// classify buckets candidates (already deduplicated, original casing) into
// new/duplicate/suppressed using existing/suppressed (both keyed by the
// SQL-normalized lower(trim(...)) form), building bounded samples as it
// goes. Duplicate is checked before suppressed: an address that is somehow
// both (a live subscribers row exists AND a suppressions row exists for
// it — the suppression added after signup, e.g. a later hard bounce) is a
// duplicate first and foremost — "already present, never overwritten" — the
// suppression fact about it is not this import's concern once "skip, it
// exists" already applies.
func classify(candidates []string, existing, suppressed map[string]bool) PreviewResult {
	var res PreviewResult
	for _, email := range candidates {
		key := strings.ToLower(strings.TrimSpace(email))
		switch {
		case existing[key]:
			res.DuplicateCount++
			if len(res.SampleDuplicate) < importSampleSize {
				res.SampleDuplicate = append(res.SampleDuplicate, email)
			}
		case suppressed[key]:
			res.SuppressedCount++
			if len(res.SampleSuppressed) < importSampleSize {
				res.SampleSuppressed = append(res.SampleSuppressed, email)
			}
		default:
			res.NewCount++
			if len(res.SampleNew) < importSampleSize {
				res.SampleNew = append(res.SampleNew, email)
			}
		}
	}
	return res
}

// unknownSlugs returns, sorted and deduplicated, every interest slug rows
// name that is not a key of knownSlugs.
func unknownSlugs(rows []ImportRow, knownSlugs map[string]bool) []string {
	seen := map[string]bool{}
	for _, r := range rows {
		for _, slug := range r.InterestSlugs {
			slug = strings.ToLower(strings.TrimSpace(slug))
			if slug == "" || knownSlugs[slug] {
				continue
			}
			seen[slug] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ImportStore is the data-access layer over subscriber_imports, and the
// committer for the subscribers rows an import produces.
type ImportStore struct {
	pool   *pgxpool.Pool
	outbox *outbox.Store
}

// NewImportStore constructs an ImportStore over the shared connection pool.
// It also constructs its own *outbox.Store over the same pool (#0129,
// mirroring NewStore's identical reasoning above it in store.go): invite
// mode's Commit must enqueue an invitation inside the SAME transaction that
// inserts the subscriber row, and internal/outbox is a leaf package with no
// dependency back on this one, so wiring it internally here keeps this
// constructor's signature unchanged for cmd/opencircuit/main.go's existing
// call site.
func NewImportStore(pool *pgxpool.Pool) *ImportStore {
	return &ImportStore{pool: pool, outbox: outbox.NewStore(pool)}
}

// Preview classifies rows against the CURRENT subscribers/suppressions
// tables, writing nothing. knownSlugs is the closed set of interest slugs
// that exist today (lowercase) — the caller builds it from
// interests.Store, since this package imports nothing internal.
func (s *ImportStore) Preview(ctx context.Context, rows []ImportRow, knownSlugs map[string]bool) (PreviewResult, error) {
	candidates := dedupeCandidates(rows)
	existing, err := existingEmails(ctx, s.pool, candidates)
	if err != nil {
		return PreviewResult{}, err
	}
	suppressed, err := suppressedEmails(ctx, s.pool, candidates)
	if err != nil {
		return PreviewResult{}, err
	}
	res := classify(candidates, existing, suppressed)
	res.UnknownInterestSlugs = unknownSlugs(rows, knownSlugs)
	return res, nil
}

const importColumns = `id, source, source_detail, consent_mode, consent_note,
	collected_at, filename, row_count, inserted_count, skipped_count,
	invited_count, confirmed_count, status, revoked_at, revoked_reason,
	imported_by, created_at`

func scanImport(row pgx.Row) (Import, error) {
	var imp Import
	err := row.Scan(
		&imp.ID, &imp.Source, &imp.SourceDetail, &imp.ConsentMode, &imp.ConsentNote,
		&imp.CollectedAt, &imp.Filename, &imp.RowCount, &imp.InsertedCount, &imp.SkippedCount,
		&imp.InvitedCount, &imp.ConfirmedCount, &imp.Status, &imp.RevokedAt, &imp.RevokedReason,
		&imp.ImportedBy, &imp.CreatedAt,
	)
	if err != nil {
		return Import{}, err
	}
	return imp, nil
}

// CommitInput is Commit's input: the batch's declared provenance plus the
// rows to insert. ConsentMode must be one of ConsentModePriorConsent or
// ConsentModeInvite — see the package doc comment.
type CommitInput struct {
	Source           string
	SourceDetail     string
	ConsentMode      string
	ConsentNote      string
	CollectedAt      time.Time
	Filename         string
	Rows             []ImportRow
	InterestSlugToID map[string]int64 // known slugs only; unknown ones are silently not linked (already reported by Preview)
	ImportedBy       *int64
}

// CommitResult is Commit's return value.
type CommitResult struct {
	Import         Import
	InsertedEmails []string
}

// Commit validates in, then — in a single transaction — inserts the
// subscriber_imports row, re-derives new/duplicate/suppressed against the
// CURRENT tables (not trusting a prior Preview call's counts; see the
// package doc comment), and inserts one subscribers row per new address,
// links known interest slugs, and writes one subscriber_events ActionImported
// row per inserted address. subscriber_imports.row_count/inserted_count/
// skipped_count/invited_count are stamped from what actually happened. A
// failure anywhere in this rolls back EVERYTHING, including the
// subscriber_imports row itself — "import is transactional" per #0125's
// acceptance criteria.
//
// The two consent modes diverge on exactly what that inserted row looks
// like:
//
//   - prior_consent (#0125): status=active, source=SubscriberSourceImport,
//     consent_basis=ConsentBasisImportedPriorConsent, import_id=the new
//     batch, source_detail copied from in.SourceDetail so a single
//     subscriber row is self-sufficient without a join back to this table,
//     confirmed_at left NULL (#0292 — see that branch's own comment below).
//     No mail is sent — prior_consent's entire point (PRD §6.10, decided
//     2026-08-21) — so this branch never touches internal/outbox.
//   - invite (#0129): status=pending with a fresh confirm_token/
//     confirm_expires_at (importInviteConfirmTTL), consent_basis left NULL
//     (PRD §6.10.1 step 2 — an invited address has been ASKED, not yet
//     consented), invited_at stamped once, and exactly one
//     outbox.KindImportInvite message enqueued in this SAME transaction —
//     the identical "a committed row can never have an unsent invitation"
//     property #0126 established for Store.Create's confirmation. The
//     address only becomes active, and consent_basis only becomes
//     ConsentBasisDoubleOptIn, when Confirm (store.go) is later called with
//     this SAME confirm_token — the public double opt-in endpoint, not a
//     second confirmation path (#0129's acceptance criteria).
func (s *ImportStore) Commit(ctx context.Context, in CommitInput, now time.Time) (CommitResult, error) {
	if !importSources[in.Source] {
		return CommitResult{}, ErrInvalidImportSource
	}
	if !consentModes[in.ConsentMode] {
		return CommitResult{}, ErrInvalidConsentMode
	}
	if strings.TrimSpace(in.SourceDetail) == "" {
		return CommitResult{}, ErrSourceDetailRequired
	}
	if strings.TrimSpace(in.ConsentNote) == "" {
		return CommitResult{}, ErrConsentNoteRequired
	}
	if in.CollectedAt.IsZero() {
		return CommitResult{}, ErrCollectedAtRequired
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitResult{}, fmt.Errorf("subscribers: beginning import commit tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx,
		`INSERT INTO subscriber_imports
		     (source, source_detail, consent_mode, consent_note, collected_at,
		      filename, row_count, status, imported_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING `+importColumns,
		// source_detail is passed directly, not through nullIfEmpty: #0291
		// makes it mandatory, validated non-blank above, so — matching
		// consent_note's own treatment just after it — there is no empty
		// case left to map to SQL NULL.
		in.Source, in.SourceDetail, in.ConsentMode, in.ConsentNote, in.CollectedAt,
		nullIfEmpty(in.Filename), len(in.Rows), ImportStatusCommitted, in.ImportedBy, now,
	)
	imp, err := scanImport(row)
	if err != nil {
		return CommitResult{}, fmt.Errorf("subscribers: creating import: %w", err)
	}

	candidates := dedupeCandidates(in.Rows)
	byEmail := make(map[string]ImportRow, len(in.Rows))
	for _, r := range in.Rows {
		key := strings.ToLower(strings.TrimSpace(r.Email))
		if _, ok := byEmail[key]; !ok {
			byEmail[key] = r
		}
	}

	existing, err := existingEmails(ctx, tx, candidates)
	if err != nil {
		return CommitResult{}, err
	}
	suppressed, err := suppressedEmails(ctx, tx, candidates)
	if err != nil {
		return CommitResult{}, err
	}

	var inserted, skipped, invited int
	var insertedEmails []string
	for _, email := range candidates {
		key := strings.ToLower(strings.TrimSpace(email))
		if existing[key] || suppressed[key] {
			skipped++
			continue
		}

		manageToken, err := newToken()
		if err != nil {
			return CommitResult{}, err
		}

		var subID int64
		var normEmail string
		// confirmToken/confirmExpiresAt are only set on the invite branch —
		// EnqueueTx (below, invite-only) reads confirmToken directly, so it
		// stays a local var rather than round-tripping through the RETURNING
		// row (the row's own confirm_token column is write-only from this
		// method's point of view; nothing here needs to read it back).
		var confirmToken string

		// A switch with an explicit default, not an if/else — #0125's
		// original ErrConsentModeNotSupported refusal existed precisely so
		// a mode with no committer could never silently fall through into a
		// prior_consent-shaped write; consentModes today admits only these
		// two values (validated above), so the default is unreachable BY
		// CONSTRUCTION, not by omission — but it stays here, loud, so a
		// future third consent_mode value added to consentModes without a
		// corresponding case here fails this Commit call with a named error
		// instead of silently taking the prior_consent branch (CLAUDE.md §9:
		// consent provenance is the whole point of this file).
		switch in.ConsentMode {
		case ConsentModeInvite:
			tok, err := newToken()
			if err != nil {
				return CommitResult{}, err
			}
			confirmToken = tok
			confirmExpiresAt := now.Add(importInviteConfirmTTL)

			// consent_basis is left NULL (PRD §6.10.1 step 2: "inserted
			// pending with a confirm_token... consent_basis = NULL") — an
			// invited address has been ASKED, not yet consented; Confirm
			// (store.go) is what sets it once the recipient follows this
			// SAME confirm_token. invited_at is stamped exactly once, here,
			// at insert — nothing else in this package ever writes it again
			// (see the package doc comment's "one invitation per address,
			// ever" note), which is what makes it write-once by
			// construction rather than by an added constraint.
			subRow := tx.QueryRow(ctx,
				`INSERT INTO subscribers
				     (email, status, confirm_token, confirm_expires_at, manage_token,
				      source, source_detail, consent_basis, import_id, invited_at,
				      created_at, updated_at)
				 VALUES
				     (lower(trim($1)), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
				 RETURNING id, email`,
				email, StatusPending, confirmToken, confirmExpiresAt, manageToken,
				SubscriberSourceImport, in.SourceDetail, nil /* consent_basis */, imp.ID, now, /* invited_at */
				now,
			)
			if err := subRow.Scan(&subID, &normEmail); err != nil {
				return CommitResult{}, fmt.Errorf("subscribers: inserting invited subscriber %q: %w", email, err)
			}
		case ConsentModePriorConsent:
			// confirmed_at is left NULL, not
			// stamped to now (#0292): PRD §6.10 says outright that a
			// prior_consent import's subscribers "did not confirm here", so
			// a local confirmation timestamp would assert something that
			// never happened — a person who never saw a confirmation link
			// cannot have "confirmed" at a moment this process picked for
			// them. This also keeps confirmed_at's existing meaning intact
			// everywhere else it's read: NULL already means "no local
			// double-opt-in confirmation event", exactly as it does for a
			// pending/unsubscribed/bounced/complained row (see
			// internal/handlers/admin_subscribers_export.go's
			// formatExportTime doc comment) — an active-but-never-locally-
			// confirmed row is a new case for that meaning, not a new
			// meaning for the column. consent_basis=
			// ConsentBasisImportedPriorConsent (below) is what records WHY
			// this row is active without one, so nothing about provenance
			// is lost by leaving this NULL. See #0292's Work log for the
			// reader-by-reader audit this decision rests on — Growth30Days
			// (subscribers/store.go) and the SES staleness guard
			// (internal/handlers/ses_notifications.go) both treat a NULL
			// here exactly the way they already treat one on a pending
			// subscriber, so this is consistent with, not a new case for,
			// their existing null handling.
			var confirmedAt *time.Time
			subRow := tx.QueryRow(ctx,
				`INSERT INTO subscribers
				     (email, status, manage_token, source, source_detail, consent_basis,
				      import_id, confirmed_at, created_at, updated_at)
				 VALUES
				     (lower(trim($1)), $2, $3, $4, $5, $6, $7, $8, $9, $9)
				 RETURNING id, email`,
				email, StatusActive, manageToken, SubscriberSourceImport,
				in.SourceDetail, ConsentBasisImportedPriorConsent, imp.ID, confirmedAt, now,
			)
			if err := subRow.Scan(&subID, &normEmail); err != nil {
				return CommitResult{}, fmt.Errorf("subscribers: inserting imported subscriber %q: %w", email, err)
			}
		default:
			// Unreachable today (consentModes admits only the two cases
			// above, validated at the top of this method) — see this
			// switch's own opening comment for why this default exists
			// anyway rather than being omitted.
			return CommitResult{}, fmt.Errorf("subscribers: commit: unhandled consent mode %q despite passing validation", in.ConsentMode)
		}
		inserted++
		insertedEmails = append(insertedEmails, normEmail)

		for _, slug := range byEmail[key].InterestSlugs {
			slug = strings.ToLower(strings.TrimSpace(slug))
			interestID, ok := in.InterestSlugToID[slug]
			if !ok {
				continue // unknown slug — already reported by Preview, silently not linked
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)
				 ON CONFLICT DO NOTHING`,
				subID, interestID,
			); err != nil {
				return CommitResult{}, fmt.Errorf("subscribers: linking interest %q for imported subscriber %d: %w", slug, subID, err)
			}
		}

		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &subID,
			Email:        normEmail,
			Action:       ActionImported,
			ImportID:     &imp.ID,
			Detail:       map[string]any{"import_source": in.Source},
		}); err != nil {
			return CommitResult{}, fmt.Errorf("subscribers: recording imported event for %d: %w", subID, err)
		}

		if in.ConsentMode == ConsentModeInvite {
			invited++
			// Enqueued in this SAME transaction as the insert above —
			// #0126's "a committed signup can never have an unsent
			// confirmation" property, one step earlier in the flow: a
			// committed invited row can never have an unsent invitation.
			// ActionInviteSent is NOT written here — it is written when the
			// message actually LEAVES the queue (mirroring
			// confirmation_sent/welcome_sent's precedent, see
			// internal/mailing/outbox_worker.go's sentAction switch), not at
			// enqueue time.
			if _, err := s.outbox.EnqueueTx(ctx, tx, outbox.Item{
				Kind:         outbox.KindImportInvite,
				Recipient:    normEmail,
				SubscriberID: &subID,
				Payload: importInvitePayload{
					ConfirmToken: confirmToken,
					ManageToken:  manageToken,
					TTLSeconds:   int64(importInviteConfirmTTL.Seconds()),
					ImportSource: in.Source,
					SourceDetail: in.SourceDetail,
					CollectedAt:  in.CollectedAt,
				},
			}); err != nil {
				return CommitResult{}, fmt.Errorf("subscribers: enqueueing invite for %d: %w", subID, err)
			}
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE subscriber_imports SET inserted_count = $2, skipped_count = $3, invited_count = $4 WHERE id = $1`,
		imp.ID, inserted, skipped, invited,
	); err != nil {
		return CommitResult{}, fmt.Errorf("subscribers: stamping import counts for %d: %w", imp.ID, err)
	}
	imp.InsertedCount = inserted
	imp.SkippedCount = skipped
	imp.InvitedCount = invited

	if err := tx.Commit(ctx); err != nil {
		return CommitResult{}, fmt.Errorf("subscribers: committing import %d: %w", imp.ID, err)
	}
	return CommitResult{Import: imp, InsertedEmails: insertedEmails}, nil
}

// GetImport returns one subscriber_imports row by id.
func (s *ImportStore) GetImport(ctx context.Context, id int64) (Import, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+importColumns+` FROM subscriber_imports WHERE id = $1`, id)
	imp, err := scanImport(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Import{}, ErrImportNotFound
	case err != nil:
		return Import{}, fmt.Errorf("subscribers: getting import %d: %w", id, err)
	}
	return imp, nil
}

// Revoke moves every subscriber whose import_id matches id AND that this
// import batch is still the sole source of that row's consent to
// 'unsubscribed' (unsubscribe_source='admin'), regardless of whether the
// address has since engaged (PRD §6.10: "the question is whether we ever
// had the right to mail them"), writes one ActionImportRevoked
// subscriber_events row per address moved, and marks the import row
// 'revoked'. Revoking an already-revoked import is a no-op — alreadyRevoked
// is true, nothing is written, and the current (already-revoked) row is
// returned with a nil email slice, not an error. alreadyRevoked is a
// separate, explicit signal rather than something a caller infers from an
// empty revokedEmails slice, because "revoked zero addresses because none
// qualified" (a real transition — the import row still moves to
// status=revoked) and "already revoked, nothing happened" (a true no-op)
// are different outcomes that both produce an empty slice.
//
// #0129 widened the target set beyond #0125's original "status='active'"
// (prior_consent's only possible state for a row this import produced):
//
//   - status='pending' — an invite that has not been accepted yet. Its
//     consent has ALWAYS derived from this import (nothing else could have
//     put it there), so it is always in scope. The UPDATE below also clears
//     confirm_token/confirm_expires_at for this branch, so a stale
//     invitation link cannot later reactivate a row this action just
//     revoked — see the UPDATE's own comment.
//   - status='active' AND consent_basis=ConsentBasisImportedPriorConsent —
//     a prior_consent row, whose only consent basis IS this import's
//     attestation.
//
// Deliberately excluded: status='active' with consent_basis=
// ConsentBasisDoubleOptIn — an invited address that FOLLOWED the confirm
// link. PRD §6.10.1's own rule: "an address that confirmed is left alone,
// because its consent no longer derives from the import" — Confirm
// (store.go) is what stamps that consent_basis, independently of anything
// this import batch did.
func (s *ImportStore) Revoke(ctx context.Context, id int64, reason string, now time.Time) (imp Import, revokedEmails []string, alreadyRevoked bool, err error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Import{}, nil, false, ErrRevokeReasonRequired
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Import{}, nil, false, fmt.Errorf("subscribers: beginning revoke tx for import %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `SELECT `+importColumns+` FROM subscriber_imports WHERE id = $1 FOR UPDATE`, id)
	imp, err = scanImport(row)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Import{}, nil, false, ErrImportNotFound
	case err != nil:
		return Import{}, nil, false, fmt.Errorf("subscribers: reading import %d for revoke: %w", id, err)
	}

	if imp.Status == ImportStatusRevoked {
		// No-op, not an error, per #0125's acceptance criteria. Nothing was
		// written, so a plain read-only commit (or rollback — equivalent
		// here) is fine.
		return imp, nil, true, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT id, email FROM subscribers
		  WHERE import_id = $1
		    AND (status = $2 OR (status = $3 AND consent_basis = $4))`,
		id, StatusPending, StatusActive, ConsentBasisImportedPriorConsent,
	)
	if err != nil {
		return Import{}, nil, false, fmt.Errorf("subscribers: selecting revocable imported subscribers for %d: %w", id, err)
	}
	type target struct {
		id    int64
		email string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.email); err != nil {
			rows.Close()
			return Import{}, nil, false, fmt.Errorf("subscribers: scanning revoke target: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Import{}, nil, false, fmt.Errorf("subscribers: iterating revoke targets: %w", err)
	}
	rows.Close()

	for _, t := range targets {
		// confirm_token/confirm_expires_at are cleared unconditionally here
		// (#0129) — a no-op for a former prior_consent row (already NULL,
		// since that mode never issues a confirm_token), and load-bearing
		// for a former invite row: without this, a stale invitation link a
		// recipient still holds would let Confirm (store.go) reactivate a
		// row this action just revoked.
		if _, err := tx.Exec(ctx,
			`UPDATE subscribers
			    SET status = $2, unsubscribed_at = $3, unsubscribe_source = $4,
			        confirm_token = NULL, confirm_expires_at = NULL, updated_at = $3
			  WHERE id = $1`,
			t.id, StatusUnsubscribed, now, SourceAdmin,
		); err != nil {
			return Import{}, nil, false, fmt.Errorf("subscribers: unsubscribing %d for import revoke: %w", t.id, err)
		}
		if err := RecordEventTx(ctx, tx, Event{
			SubscriberID: &t.id,
			Email:        t.email,
			Action:       ActionImportRevoked,
			ImportID:     &id,
			Detail:       map[string]any{"reason": reason},
		}); err != nil {
			return Import{}, nil, false, fmt.Errorf("subscribers: recording import_revoked event for %d: %w", t.id, err)
		}
		revokedEmails = append(revokedEmails, t.email)
	}

	revokedRow := tx.QueryRow(ctx,
		`UPDATE subscriber_imports SET status = $2, revoked_at = $3, revoked_reason = $4
		  WHERE id = $1
		 RETURNING `+importColumns,
		id, ImportStatusRevoked, now, reason,
	)
	imp, err = scanImport(revokedRow)
	if err != nil {
		return Import{}, nil, false, fmt.Errorf("subscribers: marking import %d revoked: %w", id, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Import{}, nil, false, fmt.Errorf("subscribers: committing revoke of import %d: %w", id, err)
	}
	return imp, revokedEmails, false, nil
}
