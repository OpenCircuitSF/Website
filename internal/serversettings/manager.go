package serversettings

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// FieldState is one field as shown to an admin.
type FieldState struct {
	Field
	Source           Source
	Value            string
	EffectiveDefault string
	PendingRestart   bool
}

// GroupState is one group's fields and any group-level error.
type GroupState struct {
	Name, Error string
	Fields      []FieldState
}

// Snapshot is the whole state, groups and fields in display order.
type Snapshot struct{ Groups []GroupState }

// Applied lists the sorted keys that really changed.
type Applied struct{ Set, Cleared []string }

type store interface {
	All(ctx context.Context) (map[string]string, error)
	Apply(ctx context.Context, set map[string]string, clear []string, now time.Time) error
}

// Group error texts for the media group. Fixed strings; a probe error is never
// echoed.
const (
	errMediaUnset       = "No media directory is set, so image uploads are refused. Save one here; the service must also be able to write to it (scripts/enable-media-upload.sh)."
	errMediaNotWritable = "The service cannot write to this directory, so image uploads are refused. Run scripts/enable-media-upload.sh on the server, or choose a directory the service can write to."
)

// Manager holds the effective values, rebuilt from the database on every
// change. Readers never lock.
type Manager struct {
	store    store
	env      map[string]string // field key -> raw env value
	defaults map[string]string // per-instance overrides of Field.Default
	probe    func(dir string) error
	log      *slog.Logger

	mu   sync.Mutex // serialises Reload and Apply
	cur  atomic.Pointer[state]
	boot atomic.Pointer[map[string]string] // effective values at the first successful Reload
}

// state is immutable; a new one replaces it on every reload.
type state struct {
	values  map[string]string
	sources map[string]Source
}

// NewManager returns a manager whose values are all unset until Reload runs.
// env maps field keys to raw environment values; defaults overrides a field's
// static default when the key is present. probe, when non-nil, checks the
// media directory for writability.
func NewManager(st store, env, defaults map[string]string, probe func(string) error, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{store: st, env: env, defaults: defaults, probe: probe, log: log}
	m.cur.Store(&state{values: map[string]string{}, sources: map[string]Source{}})
	return m
}

func (m *Manager) effectiveDefault(f Field) string {
	if d, ok := m.defaults[f.Key]; ok {
		return d
	}
	return f.Default
}

// Reload reads the saved values and recomputes every effective value.
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
	next := &state{values: map[string]string{}, sources: map[string]Source{}}
	for _, f := range Fields {
		def := m.effectiveDefault(f)
		if v, ok := saved[f.Key]; ok {
			next.values[f.Key], next.sources[f.Key] = v, SourceDatabase
			continue
		}
		env := strings.TrimSpace(m.env[f.Key])
		switch {
		case env != "" && env == def:
			next.values[f.Key], next.sources[f.Key] = env, SourceDefault
		case env != "":
			next.values[f.Key], next.sources[f.Key] = env, SourceEnvironment
		case def != "":
			next.values[f.Key], next.sources[f.Key] = def, SourceDefault
		default:
			next.values[f.Key], next.sources[f.Key] = "", SourceUnset
		}
	}
	m.cur.Store(next)
	if m.boot.Load() == nil {
		b := make(map[string]string, len(next.values))
		for k, v := range next.values {
			b[k] = v
		}
		m.boot.Store(&b)
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
	m.log.Info("server settings: saved", "set", setKeys, "cleared", clear)
	return Applied{Set: setKeys, Cleared: clear}, nil
}

// Snapshot returns the current state.
func (m *Manager) Snapshot() Snapshot {
	s := m.cur.Load()
	var boot map[string]string
	if b := m.boot.Load(); b != nil {
		boot = *b
	}
	var snap Snapshot
	for _, g := range Groups {
		gs := GroupState{Name: g}
		for _, f := range Fields {
			if f.Group != g {
				continue
			}
			fs := FieldState{Field: f, Source: s.sources[f.Key], Value: s.values[f.Key], EffectiveDefault: m.effectiveDefault(f)}
			if fs.Source == "" {
				fs.Source = SourceUnset
			}
			fs.PendingRestart = f.RestartRequired && boot != nil && s.values[f.Key] != boot[f.Key]
			gs.Fields = append(gs.Fields, fs)
		}
		if g == GroupMedia {
			dir := s.values[KeyMediaDir]
			switch {
			case dir == "":
				gs.Error = errMediaUnset
			case m.probe != nil && m.probe(dir) != nil:
				gs.Error = errMediaNotWritable
			}
		}
		snap.Groups = append(snap.Groups, gs)
	}
	return snap
}

// Value returns the current effective value for key.
func (m *Manager) Value(key string) string { return m.cur.Load().values[key] }

// MediaDir returns the current media directory; "" when none is set.
func (m *Manager) MediaDir() string { return m.Value(KeyMediaDir) }

// SESSandbox reports the SES sandbox flag. A value that does not parse counts
// as true, the safe assumption and the config default.
func (m *Manager) SESSandbox() bool {
	b, err := strconv.ParseBool(m.Value(KeySESSandbox))
	if err != nil {
		return true
	}
	return b
}

// SESInboundBucket returns the current inbound mail bucket name.
func (m *Manager) SESInboundBucket() string { return m.Value(KeySESInboundBucket) }

// BlueskyCardURL reports which URL the Bluesky external card points at:
// CardURLShort (tracked, the default) or CardURLCanonical.
func (m *Manager) BlueskyCardURL() string {
	if m.Value(KeyBlueskyCardURL) == CardURLCanonical {
		return CardURLCanonical
	}
	return CardURLShort
}

// OutreachHashtags returns the site-wide hashtag line appended to drafts.
func (m *Manager) OutreachHashtags() string { return m.Value(KeyOutreachHashtags) }

// OutreachPostHour returns the Pacific hour suggested times use; 10 when the
// value does not parse.
func (m *Manager) OutreachPostHour() int {
	n, err := strconv.Atoi(m.Value(KeyOutreachPostHour))
	if err != nil || n < 0 || n > 23 {
		return 10
	}
	return n
}
