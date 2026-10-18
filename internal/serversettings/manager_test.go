package serversettings

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	rows    map[string]string
	applies int
	err     error
}

func newFake() *fakeStore { return &fakeStore{rows: map[string]string{}} }

func (f *fakeStore) All(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]string{}
	for k, v := range f.rows {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) Apply(_ context.Context, set map[string]string, clear []string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies++
	for k, v := range set {
		f.rows[k] = v
	}
	for _, k := range clear {
		delete(f.rows, k)
	}
	return nil
}

func field(s Snapshot, key string) FieldState {
	for _, g := range s.Groups {
		for _, f := range g.Fields {
			if f.Key == key {
				return f
			}
		}
	}
	return FieldState{}
}

func group(s Snapshot, name string) GroupState {
	for _, g := range s.Groups {
		if g.Name == name {
			return g
		}
	}
	return GroupState{}
}

func load(t *testing.T, st *fakeStore, env, defs map[string]string, probe func(string) error) *Manager {
	t.Helper()
	m := NewManager(st, env, defs, probe, nil)
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPrecedence(t *testing.T) {
	st := newFake()
	st.rows[KeyEmailFrom] = "db@example.org"
	m := load(t, st, map[string]string{KeyEmailFrom: "env@example.org", KeyEmailReplyTo: "env@example.org", KeySendBatchSize: "50"}, nil, nil)
	s := m.Snapshot()
	if f := field(s, KeyEmailFrom); f.Source != SourceDatabase || f.Value != "db@example.org" {
		t.Errorf("db: %+v", f)
	}
	if f := field(s, KeyEmailReplyTo); f.Source != SourceEnvironment {
		t.Errorf("env: %+v", f)
	}
	if f := field(s, KeySendBatchSize); f.Source != SourceDefault {
		t.Errorf("env equal to default must report default: %+v", f)
	}
	if f := field(s, KeySESSandbox); f.Source != SourceDefault || f.Value != "true" {
		t.Errorf("default: %+v", f)
	}
	if f := field(s, KeyEmailListDomain); f.Source != SourceUnset || f.Value != "" {
		t.Errorf("unset: %+v", f)
	}
}

func TestDefaultsOverride(t *testing.T) {
	m := load(t, newFake(), nil, nil, nil)
	if f := field(m.Snapshot(), KeyMediaDir); f.Source != SourceDefault || f.Value != DefaultMediaDir {
		t.Errorf("no override: %+v", f)
	}
	m = load(t, newFake(), nil, map[string]string{KeyMediaDir: ""}, nil)
	if f := field(m.Snapshot(), KeyMediaDir); f.Source != SourceUnset || f.Value != "" {
		t.Errorf("override: %+v", f)
	}
}

func TestApplyClearAndNoop(t *testing.T) {
	st := newFake()
	m := load(t, st, map[string]string{KeyEmailReplyTo: "env@example.org"}, nil, nil)
	ctx := context.Background()
	if _, err := m.Apply(ctx, map[string]string{KeyEmailReplyTo: "new@example.org"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f := field(m.Snapshot(), KeyEmailReplyTo); f.Source != SourceDatabase {
		t.Fatalf("%+v", f)
	}
	n := st.applies
	a, err := m.Apply(ctx, map[string]string{KeyEmailReplyTo: "new@example.org", KeyMediaDir: " "}, time.Now())
	if err != nil || len(a.Set)+len(a.Cleared) != 0 || st.applies != n {
		t.Fatalf("no-op wrote: %+v %v", a, err)
	}
	a, err = m.Apply(ctx, map[string]string{KeyEmailReplyTo: ""}, time.Now())
	if err != nil || len(a.Cleared) != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	if f := field(m.Snapshot(), KeyEmailReplyTo); f.Source != SourceEnvironment || f.Value != "env@example.org" {
		t.Errorf("clear must revert to env: %+v", f)
	}
}

func TestApplyAllOrNothing(t *testing.T) {
	st := newFake()
	m := load(t, st, nil, nil, nil)
	_, err := m.Apply(context.Background(), map[string]string{KeyEmailFrom: "ok@example.org", KeySendBatchSize: "9000"}, time.Now())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Key != KeySendBatchSize {
		t.Fatalf("err = %v", err)
	}
	if st.applies != 0 || len(st.rows) != 0 {
		t.Error("nothing may be written")
	}
}

func TestLiveGetters(t *testing.T) {
	m := load(t, newFake(), nil, map[string]string{KeyMediaDir: ""}, nil)
	if m.MediaDir() != "" || !m.SESSandbox() || m.SESInboundBucket() != "" {
		t.Fatal("initial")
	}
	if _, err := m.Apply(context.Background(), map[string]string{KeyMediaDir: "/srv/m", KeySESSandbox: "false", KeySESInboundBucket: "my-bucket"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if m.MediaDir() != "/srv/m" || m.SESSandbox() || m.SESInboundBucket() != "my-bucket" {
		t.Fatal("after apply")
	}
}

func TestPendingRestart(t *testing.T) {
	m := load(t, newFake(), map[string]string{KeyEmailFrom: "boot@example.org"}, nil, nil)
	ctx := context.Background()
	pending := func(k string) bool { return field(m.Snapshot(), k).PendingRestart }
	m.Apply(ctx, map[string]string{KeyEmailFrom: "other@example.org"}, time.Now())
	if !pending(KeyEmailFrom) {
		t.Error("changed restart field must be pending")
	}
	m.Apply(ctx, map[string]string{KeyEmailFrom: "boot@example.org"}, time.Now())
	if pending(KeyEmailFrom) {
		t.Error("saving the boot value back must clear pending")
	}
	m.Apply(ctx, map[string]string{KeyMediaDir: "/srv/m"}, time.Now())
	if pending(KeyMediaDir) {
		t.Error("a live field is never pending")
	}
}

func TestMediaGroupError(t *testing.T) {
	m := load(t, newFake(), nil, map[string]string{KeyMediaDir: ""}, func(string) error { return errors.New("secret-detail") })
	if e := group(m.Snapshot(), GroupMedia).Error; e != errMediaUnset {
		t.Errorf("empty dir: %q", e)
	}
	m.Apply(context.Background(), map[string]string{KeyMediaDir: "/srv/m"}, time.Now())
	if e := group(m.Snapshot(), GroupMedia).Error; e != errMediaNotWritable {
		t.Errorf("probe failure: %q", e)
	}
	m = load(t, newFake(), nil, nil, nil)
	if e := group(m.Snapshot(), GroupMedia).Error; e != "" {
		t.Errorf("nil probe: %q", e)
	}
	m = load(t, newFake(), nil, nil, func(string) error { return nil })
	if e := group(m.Snapshot(), GroupMedia).Error; e != "" {
		t.Errorf("good probe: %q", e)
	}
}

func TestReloadError(t *testing.T) {
	st := newFake()
	st.err = errors.New("db down")
	if err := NewManager(st, nil, nil, nil, nil).Reload(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestConcurrentApplyAndRead(t *testing.T) {
	m := load(t, newFake(), nil, nil, nil)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.MediaDir()
					_ = m.Snapshot()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		v := "/srv/a"
		if i%2 == 0 {
			v = "/srv/b"
		}
		if _, err := m.Apply(context.Background(), map[string]string{KeyMediaDir: v}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
