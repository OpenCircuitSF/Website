// Package testdb provides cross-process serialization for integration tests
// that share a single PostgreSQL test database (TEST_DATABASE_URL), a
// shared way to open the one connection pool a package's whole test binary
// should use (see Connect), and a source of unique test identifiers that
// does not depend on clock resolution (see Unique).
//
// `go test` runs each package's test binary concurrently. Several packages
// truncate the same shared tables in their setup, so concurrent runs corrupt
// each other's data. Each such package calls Lock from its TestMain to hold a
// PostgreSQL session-level advisory lock for the duration of its test run; only
// one package can hold the lock at a time, so they run one-at-a-time even under
// `go test ./...`. When TEST_DATABASE_URL is unset, the DB-backed tests skip
// themselves and Lock is a no-op.
package testdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey is an arbitrary fixed key shared by every package that
// serializes through this helper. All callers must use the same value.
const advisoryLockKey int64 = 0x53484F52544C4B // "SHORTLK"

// Lock acquires the shared advisory lock and returns a release function the
// caller must invoke before exiting (typically right before os.Exit). It blocks
// until the lock is free. If TEST_DATABASE_URL is unset, it returns a no-op
// release immediately.
func Lock() func() {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		return func() {}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		// Surface the problem but don't block the run: the DB-backed tests will
		// fail loudly on their own connection attempts.
		fmt.Fprintf(os.Stderr, "testdb: connect failed: %v\n", err)
		return func() {}
	}

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		fmt.Fprintf(os.Stderr, "testdb: advisory lock failed: %v\n", err)
		_ = conn.Close(ctx)
		return func() {}
	}

	return func() {
		_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
		_ = conn.Close(ctx)
	}
}

// Connect opens the single pgxpool.Pool a DB-backed package's whole test
// binary should share, rather than the pre-#0091 pattern of one fresh pool
// per test.
//
// Call it once from TestMain, after Lock, and store the result in a
// package-level variable that every test's own pool helper returns; do not
// call it per test. `go test` runs each package as its own process, so "one
// package-level pool" falls out naturally from "call this once in TestMain"
// — no cross-process coordination is needed here, unlike Lock.
//
// Connect returns (nil, nil) when TEST_DATABASE_URL is unset. Callers must
// treat a nil pool as the signal to skip DB-backed tests (t.Skip), matching
// the pre-#0091 per-test behavior, rather than treating it as a connect
// failure.
func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		return nil, nil
	}

	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(connectCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("testdb: connect test db: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("testdb: ping test db: %w", err)
	}
	return pool, nil
}

// TruncateTimeout bounds a single TestMain-time entry TRUNCATE — the same
// class of operation, and the same value, that #0084 settled on for
// internal/handlers' and cmd/opencircuit's own single-DB-statement bounds
// (handlersDBOpTimeout, wiringDBOpTimeout: both 20s, sized by direct
// measurement against realistic concurrent-agent load — see issues/0084.md's
// Work log). #0097 item 2 flagged the entry truncates several packages'
// TestMain added when #0091 moved them onto one shared pool per package run:
// each had grown its own, differently-valued fixed 10s deadline — a second
// arbitrary number for the exact same kind of statement #0084 already
// tuned. Centralizing the constant here, and running the truncate through
// EntryTruncate below, is what keeps a future change to the policy from
// requiring an N-package hunt for uncoordinated literals.
const TruncateTimeout = 20 * time.Second

// EntryTruncate runs sqlStmt — a TestMain-time entry TRUNCATE — against pool
// under TruncateTimeout, and is what a DB-backed package's TestMain should
// call instead of composing its own context.WithTimeout + Exec, so every
// package's entry truncate shares one deadline, one diagnostic, and one
// failure path.
//
// tables names the relations sqlStmt truncates literally — exactly what
// appears in the TRUNCATE statement itself. EntryTruncate computes the
// TRUNCATE ... CASCADE closure of tables from pg_constraint on its own (see
// cascadeClosure) before diagnosing a failure, so callers no longer need to
// hand-enumerate CASCADE-reached relations and cannot get that enumeration
// wrong the way three of four #0097-era call sites originally did — see
// issues/0097.md's Review notes for the reproduction (a hand-written list
// missed 6, 4, and 3 relations respectively, and the diagnostic below told
// the reader, wrongly, that a real lock wait "was not a lock wait at all"
// because the blocker was holding a lock on exactly one of the omitted
// tables). tables is used only to diagnose a failure (see below), not to
// build the statement; callers still write their own SQL so the truncated
// tables stay visible and reviewable at the call site.
//
// On success EntryTruncate returns normally and the caller proceeds to
// m.Run(). On failure — most plausibly a lock wait against another session's
// open transaction on one of tables or a relation reachable from it, since
// #0091 moved every DB-backed package onto a pool shared for its whole run
// against one shared database — it:
//
//  1. runs a short diagnostic query naming every session currently holding a
//     granted lock on one of tables or the CASCADE closure computed from
//     them (pid, state, query text, query_start, and every such relation it
//     holds a lock on), so the failure names its culprit instead of just
//     "context deadline exceeded" — the blocking pid and the table are the
//     two facts #0097 item 3 asked for;
//  2. prints why the whole test binary is about to exit — a TestMain that
//     cannot guarantee its package's tables start clean cannot let any test
//     in that package run at all;
//  3. closes pool, calls release (releasing the advisory lock so it does not
//     also wedge every other package queued behind Lock), and os.Exit(1)s.
//
// EntryTruncate does not return on failure, so callers do not need their own
// truncErr-handling branch.
func EntryTruncate(pool *pgxpool.Pool, release func(), sqlStmt string, tables []string) {
	ctx, cancel := context.WithTimeout(context.Background(), TruncateTimeout)
	_, err := pool.Exec(ctx, sqlStmt)
	cancel()
	if err == nil {
		return
	}

	fmt.Fprintf(os.Stderr, "testdb: entry truncate failed: %v\n", err)

	closure, closureErr := cascadeClosure(pool, tables)
	if closureErr != nil {
		fmt.Fprintf(os.Stderr, "testdb: cascade closure lookup failed (%v); diagnosing only the "+
			"literally-named relations %s, which may miss a CASCADE-reached culprit\n",
			closureErr, strings.Join(tables, ", "))
		closure = tables
	}

	if diag := diagnoseLockHolders(pool, closure); diag != "" {
		fmt.Fprintf(os.Stderr, "testdb: session(s) holding a lock on %s at diagnosis time:\n%s",
			strings.Join(closure, ", "), diag)
	} else {
		// Three possibilities, not two (#0225 review): the blocker may have
		// released between the timeout and this check, it may be holding a
		// lock this diagnostic did not check for, or — a case the previous
		// wording didn't name — the original failure above may not have been
		// a lock wait at all. That third case isn't actually misleading (the
		// real error from pool.Exec printed on the line above this block),
		// but it should still be named here rather than left implicit.
		fmt.Fprintf(os.Stderr, "testdb: no lock holder found on %s at diagnosis time — "+
			"either the blocker released between the timeout and this check, it is holding "+
			"a lock this diagnostic did not check for, or the original failure (see the error "+
			"above) was not a lock wait at all\n", strings.Join(closure, ", "))
	}
	fmt.Fprintf(os.Stderr, "testdb: exiting this test binary (os.Exit(1)) — TestMain cannot "+
		"guarantee a clean starting state for %s, so no test in this package can safely run\n",
		strings.Join(closure, ", "))
	pool.Close()
	release()
	os.Exit(1)
}

// cascadeClosure returns roots plus every relation reachable by following
// foreign-key references transitively from roots — i.e. the actual set of
// tables a `TRUNCATE roots... CASCADE` locks, computed from pg_constraint
// rather than trusted from a hand-written list at each EntryTruncate call
// site.
//
// TRUNCATE ... CASCADE truncates (and locks) every table with a foreign key
// pointing at a table being truncated, regardless of that key's ON DELETE
// action — not only the ones declared ON DELETE CASCADE. #0097 item 3's
// review reproduced this concretely: internal/audit's hand-written tables
// list named only audit_log and users, but users(id) is also referenced by
// email_campaigns.created_by with no ON DELETE clause at all (a plain
// REFERENCES, migrations/000017_create_campaigns.up.sql), and
// `TRUNCATE users CASCADE` still takes an ACCESS EXCLUSIVE lock on
// email_campaigns — and, transitively, on its own two children
// campaign_interests and email_sends — to preserve referential integrity.
// A closure built only from confdeltype='c' rows would have missed exactly
// the same relations the hand-written list did, so this walks every foreign
// key (contype='f'), not only the ON DELETE CASCADE subset.
func cascadeClosure(pool *pgxpool.Pool, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return roots, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `
		SELECT conrelid::regclass::text, confrelid::regclass::text
		  FROM pg_constraint
		 WHERE contype = 'f'`)
	if err != nil {
		return nil, fmt.Errorf("testdb: cascade closure query: %w", err)
	}
	defer rows.Close()

	// childrenOf[parent] lists every relation whose foreign key points at
	// parent — i.e. every relation TRUNCATE parent CASCADE also locks.
	childrenOf := make(map[string][]string)
	for rows.Next() {
		var child, parent string
		if scanErr := rows.Scan(&child, &parent); scanErr != nil {
			return nil, fmt.Errorf("testdb: cascade closure scan: %w", scanErr)
		}
		childrenOf[parent] = append(childrenOf[parent], child)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("testdb: cascade closure row iteration: %w", rowsErr)
	}

	seen := make(map[string]bool, len(roots))
	closure := make([]string, 0, len(roots))
	queue := make([]string, 0, len(roots))
	for _, t := range roots {
		if seen[t] {
			continue
		}
		seen[t] = true
		closure = append(closure, t)
		queue = append(queue, t)
	}
	for i := 0; i < len(queue); i++ {
		for _, child := range childrenOf[queue[i]] {
			if seen[child] {
				continue
			}
			seen[child] = true
			closure = append(closure, child)
			queue = append(queue, child)
		}
	}
	return closure, nil
}

// diagnoseLockHolders queries pg_locks/pg_stat_activity for every session
// (other than this one) currently holding a granted lock on one of tables,
// and formats one line per session, naming every one of tables that session
// holds a lock on. It uses a fresh, short-lived query against pool rather
// than the caller's own (already-expired) context, since the whole point is
// to still succeed diagnostically even though the original statement could
// not.
//
// tables' entries are matched by relation identity (oid), not by formatted
// text (#0225). cascadeClosure hands back conrelid::regclass::text — quoted
// where a name needs it, schema-qualified where the relation is outside
// search_path — and a naive `pg_class.relname = ANY(tables)` (the previous
// version) does not agree with that format for either shape: it missed a
// same-quoted or non-search_path relation entirely, and would conversely
// have false-matched a same-named relation in a different schema had one
// ever been passed in raw. to_regclass() parses exactly the same syntax
// `::regclass::text` produces — quoted, schema-qualified, or bare — and
// resolves it under the same search_path rules, so pairing it with
// l.relation (already an oid, no join needed to get one) sidesteps the
// dialect mismatch entirely: identity is compared as oid = oid, and the
// display column below reconstructs a canonical (possibly quoted,
// possibly schema-qualified) name from that oid rather than trusting
// pg_class.relname to already be one. Unlike a plain `::regclass` cast,
// to_regclass() returns NULL rather than erroring for a relation that no
// longer exists (e.g. dropped between the timeout and this diagnosis), so
// a stale entry in tables degrades to "no match" instead of failing the
// whole query.
//
// Deliberately excludes advisory locks (pg_locks.relation is NULL for
// those): NULL = ANY(...) is NULL, never true, so those rows never satisfy
// the WHERE clause. The cross-package serialization lock from Lock above is
// expected to be held by whichever package is mid-run, and would otherwise
// show up as false "culprit" noise on every single diagnosis.
//
// A session holding locks on more than one of tables gets one line, not one
// per table (#0097 item 3 review: the previous version deduplicated by pid
// alone and reported only whichever relation happened to sort first for
// that pid, so a session blocking on several of the truncated tables looked
// like it was blocking on just one, arbitrarily chosen).
//
// pg_locks is cluster-wide, not scoped to the caller's database (#0234).
// scripts/testdb.sh clones each agent's test database from a shared
// template with CREATE DATABASE ... TEMPLATE, and a template clone carries
// its relation OIDs over verbatim — audit_log is the same oid in every
// clone. Without a l.database predicate, a lock held on audit_log in one
// agent's database is indistinguishable, by oid alone, from one held in
// ours: the query above would report someone else's session as the culprit
// for our stuck TRUNCATE. The filter below restricts to locks in the
// current database, but not by a naive `= current_database()`: shared
// catalog objects (e.g. pg_shdepend, pg_authid) always record
// l.database = 0 in pg_locks regardless of which database is doing the
// locking, so `= current_database()` alone would silently blind this
// diagnostic to that whole class of lock. `IN (0, <our oid>)` keeps
// shared-catalog locks visible while still excluding every other agent's
// per-database lock.
func diagnoseLockHolders(pool *pgxpool.Pool, tables []string) string {
	if len(tables) == 0 {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `
		SELECT l.pid, l.relation::regclass::text, a.state, COALESCE(a.query, ''), a.query_start
		  FROM pg_locks l
		  JOIN pg_stat_activity a ON a.pid = l.pid
		 WHERE l.granted
		   AND l.pid <> pg_backend_pid()
		   AND l.database IN (0, (SELECT oid FROM pg_database WHERE datname = current_database()))
		   AND l.relation = ANY(
		         SELECT to_regclass(t)::oid
		           FROM unnest($1::text[]) AS t
		       )
		 ORDER BY l.pid`, tables)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb: lock diagnosis query itself failed: %v\n", err)
		return ""
	}
	defer rows.Close()

	type holder struct {
		relnames   []string
		state      string
		query      string
		queryStart *time.Time
	}
	holders := make(map[int32]*holder)
	var order []int32 // first-seen order == ascending pid, per ORDER BY l.pid
	for rows.Next() {
		var (
			pid        int32
			relname    string
			state      string
			query      string
			queryStart *time.Time
		)
		if scanErr := rows.Scan(&pid, &relname, &state, &query, &queryStart); scanErr != nil {
			fmt.Fprintf(os.Stderr, "testdb: lock diagnosis scan failed: %v\n", scanErr)
			continue
		}

		h, ok := holders[pid]
		if !ok {
			h = &holder{state: state, query: query, queryStart: queryStart}
			holders[pid] = h
			order = append(order, pid)
		}
		dup := false
		for _, r := range h.relnames {
			if r == relname {
				dup = true
				break
			}
		}
		if !dup {
			h.relnames = append(h.relnames, relname)
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		fmt.Fprintf(os.Stderr, "testdb: lock diagnosis row iteration failed: %v\n", rowsErr)
	}

	var b strings.Builder
	for _, pid := range order {
		h := holders[pid]
		started := "unknown"
		if h.queryStart != nil {
			started = h.queryStart.Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "  pid %d holds a lock on %s, state=%s, query_start=%s: %s\n",
			pid, strings.Join(h.relnames, ", "), h.state, started, h.query)
	}
	return b.String()
}

// uniqueCounter backs Unique. It is process-wide (not per-package,
// per-test, or per-goroutine) and incremented atomically.
var uniqueCounter int64

// Unique returns an integer for building test-only identifiers (slugs,
// emails, titles, tokens) that must differ between calls — never a value
// meant to be read as a timestamp.
//
// It replaces the pattern of deriving "uniqueness" from
// time.Now().UnixNano() (#0158). That clock is not fine-grained enough on
// this project's hardware to serve as a uniqueness source: a 200,000-pair
// probe measured 189,484 identical consecutive readings — 94.7%, minimum
// delta 0ns — so two back-to-back calls building a slug from it are a near
// coin-flip for returning the same value, and any test that calls such a
// helper twice in a row (no intervening work) is a near coin-flip for a
// unique-constraint violation. An atomic counter has no such gap: each call
// is a distinct integer by construction, regardless of clock resolution.
//
// A bare counter is enough to make values distinct *within one process*,
// but is not on its own enough here. `go test ./...` runs each package as
// its own OS process (independent counters, each starting at 0), several of
// those processes point at the same shared TEST_DATABASE_URL, and more than
// one package mints identifiers from an identical literal prefix — e.g.
// both internal/interests and internal/handlers build interest slugs as
// "zz-test-<n>". Two such processes racing during `go test ./...` could
// each hand out counter value 1 and collide in the database even though
// neither process ever repeated a value itself. Folding in this process's
// PID (which no other concurrently running process can share) avoids that
// without requiring every call site to also thread through a package name
// or the ISSUE env var: the high 32 bits vary by process, the low 32 bits
// vary by call, and 32 bits of counter (~4 billion calls) is far more than
// any test binary will mint in a run.
func Unique() int64 {
	n := atomic.AddInt64(&uniqueCounter, 1)
	return int64(os.Getpid())<<32 | (n & 0xffffffff)
}
