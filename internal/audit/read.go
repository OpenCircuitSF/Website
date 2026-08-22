package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Record is one audit_log row read back for the admin audit view (GET
// /admin/audit). It mirrors the table's columns. Pointer fields are nullable so
// the handler can serialize an unset actor_id/user_id/target_id/ip_address as
// JSON null rather than a zero value. Metadata is the raw JSONB bytes
// (json.RawMessage) so it round-trips to the client as a JSON object, never a
// string; a nil Metadata is SQL NULL and serializes as JSON null.
//
// This is the read counterpart to Entry (the write shape). It lives in the
// audit package so the column semantics — actor_id (who acted) vs user_id
// (whose resource was affected) — stay defined in one place alongside the write
// path.
type Record struct {
	ID         int64
	ActorID    *int64
	UserID     *int64
	Action     string
	TargetType *string
	TargetID   *int64
	Metadata   json.RawMessage
	IP         *string
	CreatedAt  time.Time
}

// Reader reads audit_log rows over the shared pgx pool for the admin audit
// view. It is separate from Logger (the write path) but takes the same pool;
// construct it in main alongside the Logger.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the shared connection pool.
func NewReader(pool *pgxpool.Pool) *Reader {
	return &Reader{pool: pool}
}

// Filter narrows ListAuditLog's result. Every non-zero field is ANDed
// together; the zero value (all fields unset) returns the whole log.
//
// TargetID deliberately does NOT filter alone — a bare target_id is
// ambiguous across target types (e.g. subscriber #5 and email_campaign #5
// are unrelated rows). Callers combining TargetID must also set TargetType;
// ListAuditLog treats a TargetID with an empty TargetType as "no target_id
// filter" rather than guessing, so the handler is what enforces that
// pairing as a 400 (see internal/handlers/audit.go).
//
// This is #0114's fix: #0045's send worker writes
// audit.ActionEmailCampaignSendRefused with a NULL actor (the worker is not
// a user), so the row explaining a campaign's demotion from scheduled back
// to draft was previously unreachable through the user_id-only filter this
// type replaces. TargetType/TargetID reach it directly; UserID is untouched
// by that row and stays nil-safe to combine with the others.
type Filter struct {
	UserID     *int64
	TargetType string
	TargetID   *int64
}

// whereClause builds the SQL WHERE fragment (without the "WHERE" keyword) and
// its positional args for this filter, ANDing together whichever of
// UserID/TargetType/TargetID are set. TargetID only contributes a clause when
// TargetType is also set (see the Filter doc comment) — a bare TargetID is
// silently dropped rather than guessed at, since the handler is responsible
// for rejecting that combination before it reaches here. An all-zero Filter
// returns ("", nil): no WHERE keyword, no args.
func (f Filter) whereClause() (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if f.UserID != nil {
		args = append(args, *f.UserID)
		clauses = append(clauses, fmt.Sprintf("user_id = $%d", len(args)))
	}
	if f.TargetType != "" {
		args = append(args, f.TargetType)
		clauses = append(clauses, fmt.Sprintf("target_type = $%d", len(args)))
		if f.TargetID != nil {
			args = append(args, *f.TargetID)
			clauses = append(clauses, fmt.Sprintf("target_id = $%d", len(args)))
		}
	}
	if len(clauses) == 0 {
		return "", nil
	}
	where := clauses[0]
	for _, c := range clauses[1:] {
		where += " AND " + c
	}
	return where, args
}

// ListAuditLog returns a page of audit_log rows newest-first (created_at DESC,
// id DESC as a stable tiebreak) together with the total row count matching the
// filter. A zero-value Filter returns the whole log (using the (created_at
// DESC) index); UserID alone uses the (user_id, created_at DESC) index. Rows
// with a NULL actor_id are returned like any other row — Filter only ever
// restricts by user_id/target_type/target_id, never by actor, so a
// machine-written row (NULL actor) is reachable through TargetType/TargetID
// exactly like an operator-written one. limit/offset page the result; the
// caller is responsible for clamping limit to a sane maximum.
//
// ip_address is read as host(ip_address) so an INET like 203.0.113.7/32 comes
// back as the bare address string. metadata is read as raw JSONB bytes so it
// reaches the client as a JSON object, not a quoted string.
func (r *Reader) ListAuditLog(ctx context.Context, filter Filter, limit, offset int) (rows []Record, total int64, err error) {
	where, args := filter.whereClause()

	countSQL := "SELECT COUNT(*) FROM audit_log"
	if where != "" {
		countSQL += " WHERE " + where
	}
	if err = r.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("audit: counting audit_log: %w", err)
	}

	// limit/offset are appended after the filter args so the WHERE clause's
	// placeholder numbers stay stable between the count and page queries.
	pageSQL := `SELECT id, actor_id, user_id, action, target_type, target_id,
	                   metadata, host(ip_address), created_at
	              FROM audit_log`
	if where != "" {
		pageSQL += " WHERE " + where
	}
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	pageSQL += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", limitPos, offsetPos)
	pageArgs := append(append([]any{}, args...), limit, offset)

	queryRows, err := r.pool.Query(ctx, pageSQL, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit: querying audit_log: %w", err)
	}
	defer queryRows.Close()

	for queryRows.Next() {
		var rec Record
		var metaRaw []byte
		if err := queryRows.Scan(
			&rec.ID, &rec.ActorID, &rec.UserID, &rec.Action, &rec.TargetType,
			&rec.TargetID, &metaRaw, &rec.IP, &rec.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("audit: scanning audit_log row: %w", err)
		}
		// metaRaw is nil for a SQL NULL metadata column; leave Metadata nil so it
		// serializes as JSON null. Otherwise hand the raw JSONB bytes straight
		// through so the client receives a JSON object.
		if metaRaw != nil {
			rec.Metadata = json.RawMessage(metaRaw)
		}
		rows = append(rows, rec)
	}
	if err := queryRows.Err(); err != nil {
		return nil, 0, fmt.Errorf("audit: iterating audit_log rows: %w", err)
	}
	return rows, total, nil
}
