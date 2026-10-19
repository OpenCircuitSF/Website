package outreachconn

import (
	"context"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// Source says where a field's effective value comes from.
type Source string

// The four sources, in precedence order (unset is the absence of any).
const (
	SourceDatabase    Source = "database"
	SourceEnvironment Source = "environment"
	SourceDefault     Source = "default"
	SourceUnset       Source = "unset"
)

// FieldState is one field as shown to an admin. Value is always "" for a
// secret field.
type FieldState struct {
	Field
	Source Source
	Value  string
}

// GroupState is one service's fields and whether its client is usable.
type GroupState struct {
	Name       string
	Configured bool
	Error      string
	Fields     []FieldState
}

// Snapshot is the whole connection state, groups and fields in display order.
type Snapshot struct{ Groups []GroupState }

// Changed flags the groups whose effective configuration changed.
type Changed struct{ ShortLinks, Bluesky, Mastodon bool }

// Applied lists the sorted keys that really changed.
type Applied struct{ Set, Cleared []string }

// The outcome of a connection check from Settings. Error is always one of a
// fixed set of strings, never upstream text.
type TestResult struct {
	OK            bool
	Detail, Error string
}

type store interface {
	All(ctx context.Context) (map[string]string, error)
	Apply(ctx context.Context, set map[string]string, clear []string, now time.Time) error
}

// Group error texts shown when a group's effective configuration is rejected
// by its client constructor.
const (
	errShortLinks = "The ShortLinks base URL is not a valid URL."
	errBluesky    = "The Bluesky PDS URL is not a valid URL."
	errMastodon   = "The Mastodon instance URL must be an https URL."
)

// Manager holds the live clients, rebuilt when the effective configuration
// changes. Readers never lock.
type Manager struct {
	store store
	env   map[string]string // field key -> raw env value
	log   *slog.Logger

	mu  sync.Mutex // serialises Reload and Apply
	cur atomic.Pointer[state]

	lmu       sync.Mutex // guards listeners
	listeners []func(Changed)

	// maAcct caches the Mastodon account a successful connection test
	// verified, keyed by the config it was verified under (#0593): a changed
	// instance or token invalidates it without any explicit clear.
	maAcct atomic.Pointer[maAccount]
	// bsAcct is the same for Bluesky (#0601): the handle the session reports,
	// keyed by the PDS, sign-in identifier and app password it was verified
	// under. The saved value may be the sign-in email, which is not a handle.
	bsAcct atomic.Pointer[maAccount]

	// lookups tracks in-flight reload account lookups; tests wait on it.
	lookups sync.WaitGroup
	// lookupAccount fetches the Mastodon account on reload (#0597). It is a
	// field so a test can substitute it; the default asks the instance.
	lookupAccount func(ctx context.Context, c *mastodon.Client) (mastodon.Account, error)
	// lookupBlueskyHandle fetches the Bluesky session's handle on reload
	// (#0601); a field for the same reason.
	lookupBlueskyHandle func(ctx context.Context, c *bluesky.Client) (string, error)
}

// accountLookupTimeout bounds the reload-time verify_credentials call.
const accountLookupTimeout = 5 * time.Second

type maAccount struct{ key, acct string }

// state is an immutable snapshot; a new one replaces it on every reload.
type state struct {
	sl                  *shortlinks.Client
	bs                  *bluesky.Client
	ma                  *mastodon.Client
	slKey, bsKey, maKey string            // effective config joined with NUL; compared to decide a rebuild
	errs                map[string]string // group -> fixed error text
	values              map[string]string // effective values (secrets included; never exported)
	sources             map[string]Source
	saved               map[string]string // database rows
	anyDB               bool
}

// NewManager returns a manager whose clients are all nil until Reload runs.
// env maps field keys to the raw environment values.
func NewManager(st store, env map[string]string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{store: st, env: env, log: log}
	m.lookupAccount = func(ctx context.Context, c *mastodon.Client) (mastodon.Account, error) {
		return c.VerifyCredentials(ctx)
	}
	m.lookupBlueskyHandle = func(ctx context.Context, c *bluesky.Client) (string, error) {
		return c.VerifyCredentials(ctx)
	}
	m.cur.Store(&state{errs: map[string]string{}, values: map[string]string{}, sources: map[string]Source{}, saved: map[string]string{}})
	return m
}

// Reload reads the saved values and rebuilds every client whose effective
// configuration changed.
func (m *Manager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadLocked(ctx)
}

func (m *Manager) reloadLocked(ctx context.Context) error {
	saved, err := m.store.All(ctx)
	if err != nil {
		return err
	}
	prev := m.cur.Load()
	next := &state{errs: map[string]string{}, values: map[string]string{}, sources: map[string]Source{}, saved: saved, anyDB: len(saved) > 0}
	for _, f := range Fields {
		if v, ok := saved[f.Key]; ok {
			next.values[f.Key], next.sources[f.Key] = v, SourceDatabase
			continue
		}
		env := strings.TrimSpace(m.env[f.Key])
		switch {
		case env != "" && env == f.Default:
			next.values[f.Key], next.sources[f.Key] = env, SourceDefault
		case env != "":
			next.values[f.Key], next.sources[f.Key] = env, SourceEnvironment
		case f.Default != "":
			next.values[f.Key], next.sources[f.Key] = f.Default, SourceDefault
		default:
			next.values[f.Key], next.sources[f.Key] = "", SourceUnset
		}
	}
	v := next.values
	next.slKey = strings.Join([]string{v[KeyShortLinksBaseURL], v[KeyShortLinksAPIToken]}, "\x00")
	next.bsKey = strings.Join([]string{v[KeyBlueskyPDSURL], v[KeyBlueskyHandle], v[KeyBlueskyAppPassword]}, "\x00")
	next.maKey = strings.Join([]string{v[KeyMastodonBaseURL], v[KeyMastodonToken]}, "\x00")

	var ch Changed
	var lookup, lookupBs bool
	if next.slKey == prev.slKey && prev.slKey != "" {
		next.sl, next.errs[GroupShortLinks] = prev.sl, prev.errs[GroupShortLinks]
	} else {
		ch.ShortLinks = next.slKey != prev.slKey
		c, err := shortlinks.New(v[KeyShortLinksBaseURL], v[KeyShortLinksAPIToken])
		if err != nil {
			next.errs[GroupShortLinks] = errShortLinks
			m.log.Warn("outreach: connection misconfigured", "group", GroupShortLinks)
		}
		next.sl = c
		m.log.Info("outreach: connection reloaded", "group", GroupShortLinks, "configured", c != nil)
	}
	if next.bsKey == prev.bsKey && prev.bsKey != "" {
		next.bs, next.errs[GroupBluesky] = prev.bs, prev.errs[GroupBluesky]
	} else {
		ch.Bluesky = next.bsKey != prev.bsKey
		c, err := bluesky.New(v[KeyBlueskyPDSURL], v[KeyBlueskyHandle], v[KeyBlueskyAppPassword])
		if err != nil {
			next.errs[GroupBluesky] = errBluesky
			m.log.Warn("outreach: connection misconfigured", "group", GroupBluesky)
		}
		if c != nil && loopbackURL(v[KeyBlueskyPDSURL]) {
			// A loopback PDS is a development mock (#0580). It serves the
			// AppView reads too, so engagement counts never leave the machine
			// for the public AppView host.
			if aerr := c.SetAppViewURL(v[KeyBlueskyPDSURL]); aerr != nil {
				m.log.Warn("outreach: bluesky appview override rejected", "err", aerr)
			}
		}
		next.bs = c
		m.log.Info("outreach: connection reloaded", "group", GroupBluesky, "configured", c != nil)
		lookupBs = c != nil && !reservedHost(v[KeyBlueskyPDSURL])
	}
	if next.maKey == prev.maKey && prev.maKey != "" {
		next.ma, next.errs[GroupMastodon] = prev.ma, prev.errs[GroupMastodon]
	} else {
		ch.Mastodon = next.maKey != prev.maKey
		c, err := mastodon.New(v[KeyMastodonBaseURL], v[KeyMastodonToken])
		if err != nil {
			next.errs[GroupMastodon] = errMastodon
			m.log.Warn("outreach: connection misconfigured", "group", GroupMastodon)
		}
		next.ma = c
		m.log.Info("outreach: connection reloaded", "group", GroupMastodon, "configured", c != nil)
		lookup = c != nil && !reservedHost(v[KeyMastodonBaseURL])
	}
	m.cur.Store(next)
	if lookup {
		// Started only once next is current, so a fast answer is compared
		// against the config it was fetched under, never the previous one.
		m.lookupMastodonAccount(next.ma, v)
	}
	if lookupBs {
		m.lookupBlueskyAccount(next.bs, v)
	}
	if ch != (Changed{}) {
		m.lmu.Lock()
		ls := append([]func(Changed){}, m.listeners...)
		m.lmu.Unlock()
		for _, fn := range ls {
			fn(ch)
		}
	}
	return nil
}

// Apply validates and saves values. A value that trims to "" clears the saved
// row so the field falls back to its environment variable. All entries are
// validated before anything is written. Entries equal to the saved value, and
// clears of fields with no row, are not changes.
func (m *Manager) Apply(ctx context.Context, values map[string]string, now time.Time) (Applied, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	norm := map[string]string{}
	var wantClear []string
	for k, raw := range values {
		if strings.TrimSpace(raw) == "" {
			if _, ok := FieldByKey(k); !ok {
				return Applied{}, &ValidationError{Key: k, Msg: "unknown key"}
			}
			wantClear = append(wantClear, k)
			continue
		}
		n, err := Normalize(k, raw)
		if err != nil {
			return Applied{}, err
		}
		norm[k] = n
	}
	saved, err := m.store.All(ctx)
	if err != nil {
		return Applied{}, err
	}
	set := map[string]string{}
	var setKeys, clear []string
	for k, v := range norm {
		if old, ok := saved[k]; !ok || old != v {
			set[k] = v
			setKeys = append(setKeys, k)
		}
	}
	for _, k := range wantClear {
		if _, ok := saved[k]; ok {
			clear = append(clear, k)
		}
	}
	if len(set) == 0 && len(clear) == 0 {
		return Applied{}, nil
	}
	sort.Strings(setKeys)
	sort.Strings(clear)
	if err := m.store.Apply(ctx, set, clear, now); err != nil {
		return Applied{}, err
	}
	if err := m.reloadLocked(ctx); err != nil {
		return Applied{}, err
	}
	return Applied{Set: setKeys, Cleared: clear}, nil
}

// Account names the public account a channel posts as, from the current
// values and with no network call (#0581): for Bluesky the handle the session
// reported once a connection test or the reload-time lookup has verified it
// (#0601), the saved value before that only if it is not an email, else "";
// for Mastodon the
// "@user@host" for Mastodon once a connection test or the reload-time lookup
// (#0597) has verified the account under the current instance and token
// (cached in memory, not stored), the
// bare instance host before that, and "" for anything else or when unset.
func (m *Manager) Account(channel string) string {
	v := m.cur.Load().values
	switch channel {
	case "bluesky":
		if c := m.bsAcct.Load(); c != nil && c.key == m.cur.Load().bsKey && c.acct != "" {
			return c.acct
		}
		// Not verified yet: the stored value shows only if it is a handle. A
		// sign-in email is accepted by Bluesky but is not the account's name.
		h := strings.TrimPrefix(strings.TrimSpace(v[KeyBlueskyHandle]), "@")
		if h == "" || strings.Contains(h, "@") {
			return ""
		}
		return "@" + h
	case "mastodon":
		if c := m.maAcct.Load(); c != nil && c.key == m.cur.Load().maKey && c.acct != "" {
			return c.acct
		}
		u, err := url.Parse(strings.TrimSpace(v[KeyMastodonBaseURL]))
		if err != nil {
			return ""
		}
		return u.Host
	}
	return ""
}

// Snapshot returns the current state. Secret fields never carry a value.
func (m *Manager) Snapshot() Snapshot {
	s := m.cur.Load()
	var snap Snapshot
	for _, g := range Groups {
		gs := GroupState{Name: g, Error: s.errs[g]}
		switch g {
		case GroupShortLinks:
			gs.Configured = s.sl != nil
		case GroupBluesky:
			gs.Configured = s.bs != nil
		case GroupMastodon:
			gs.Configured = s.ma != nil
		}
		for _, f := range Fields {
			if f.Group != g {
				continue
			}
			fs := FieldState{Field: f, Source: s.sources[f.Key]}
			if fs.Source == "" {
				fs.Source = SourceUnset
			}
			if !f.Secret {
				fs.Value = s.values[f.Key]
			}
			gs.Fields = append(gs.Fields, fs)
		}
		snap.Groups = append(snap.Groups, gs)
	}
	return snap
}

// ShortLinks returns the current client; nil when not configured.
func (m *Manager) ShortLinks() *shortlinks.Client { return m.cur.Load().sl }

// Bluesky returns the current client; nil when not configured.
func (m *Manager) Bluesky() *bluesky.Client { return m.cur.Load().bs }

// Mastodon returns the current client; nil when not configured.
func (m *Manager) Mastodon() *mastodon.Client { return m.cur.Load().ma }

// HasDatabaseValues reports whether any field has a saved value.
func (m *Manager) HasDatabaseValues() bool { return m.cur.Load().anyDB }

// OnChange registers fn to run after a reload that changed a group's
// effective configuration.
func (m *Manager) OnChange(fn func(Changed)) {
	m.lmu.Lock()
	m.listeners = append(m.listeners, fn)
	m.lmu.Unlock()
}

// loopbackURL reports whether raw is an http(s) URL on localhost or a
// loopback address. Only validated saved values reach it, so a parse failure
// is simply false.
func loopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// lookupMastodonAccount fetches "@user@host" in the background so a restart
// or a settings save need not wait on the instance (#0597). It is bounded by
// accountLookupTimeout. A failure leaves Account on the bare host and logs the
// group name only: neither the token nor upstream text is ever logged.
func (m *Manager) lookupMastodonAccount(c *mastodon.Client, v map[string]string) {
	vals := map[string]string{KeyMastodonBaseURL: v[KeyMastodonBaseURL], KeyMastodonToken: v[KeyMastodonToken]}
	m.lookups.Add(1)
	go func() {
		defer m.lookups.Done()
		ctx, cancel := context.WithTimeout(context.Background(), accountLookupTimeout)
		defer cancel()
		acct, err := m.lookupAccount(ctx, c)
		if err != nil || strings.TrimSpace(acct.Acct) == "" {
			m.log.Warn("outreach: mastodon account lookup failed", "group", GroupMastodon)
			return
		}
		m.rememberMastodonAccount(vals, acct.Acct)
	}()
}

// lookupBlueskyAccount fetches the session's handle in the background, as
// lookupMastodonAccount does for Mastodon (#0601). A failure leaves Account on
// the stored value (or nothing, for an email) and logs the group name only.
func (m *Manager) lookupBlueskyAccount(c *bluesky.Client, v map[string]string) {
	vals := map[string]string{KeyBlueskyPDSURL: v[KeyBlueskyPDSURL], KeyBlueskyHandle: v[KeyBlueskyHandle], KeyBlueskyAppPassword: v[KeyBlueskyAppPassword]}
	m.lookups.Add(1)
	go func() {
		defer m.lookups.Done()
		ctx, cancel := context.WithTimeout(context.Background(), accountLookupTimeout)
		defer cancel()
		h, err := m.lookupBlueskyHandle(ctx, c)
		if err != nil || strings.TrimSpace(h) == "" {
			m.log.Warn("outreach: bluesky account lookup failed", "group", GroupBluesky)
			return
		}
		m.rememberBlueskyHandle(vals, h)
	}()
}

// reservedHost reports whether raw's host is in a TLD RFC 2606 / 6761
// reserves and can never resolve, so a lookup would only waste a DNS query.
func reservedHost(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, tld := range []string{".test", ".example", ".invalid"} {
		if strings.HasSuffix(h, tld) {
			return true
		}
	}
	return false
}
