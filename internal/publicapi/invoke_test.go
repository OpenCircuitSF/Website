package publicapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

type qIn struct {
	Query string `json:"query" query:"query"`
}

type counted struct {
	n   int
	err error
	fn  func()
}

func (c *counted) op(name string) Op {
	return Define(api.Operation{Name: name, Method: http.MethodGet, Path: Prefix + name, Summary: "S."},
		func(context.Context, qIn) (tOut, error) {
			c.n++
			if c.fn != nil {
				c.fn()
			}
			return tOut{OK: true}, c.err
		}, func(tOut) string { return "" })
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func testReg() (*Registry, *fakeClock) {
	r := NewRegistry("1")
	c := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r.now = c.now
	r.SetLogger(slog.New(slog.NewTextHandler(new(bytes.Buffer), nil)))
	return r, c
}

func call(t *testing.T, r *Registry, op Op, in any) {
	t.Helper()
	_, _ = r.Invoke(context.Background(), op, in)
}

func TestInvokeCachesMarkedOpForTTL(t *testing.T) {
	r, clk := testReg()
	c := &counted{}
	op := c.op("list_a").WithCache()
	call(t, r, op, qIn{"x"})
	call(t, r, op, qIn{"x"})
	if c.n != 1 {
		t.Fatalf("ran %d times, want 1", c.n)
	}
	clk.t = clk.t.Add(ListCacheTTL)
	call(t, r, op, qIn{"x"})
	if c.n != 2 {
		t.Fatalf("after TTL ran %d times, want 2", c.n)
	}
}

func TestInvokeCacheKeyIsOpAndParams(t *testing.T) {
	r, _ := testReg()
	a, b := &counted{}, &counted{}
	opA, opB := a.op("list_a").WithCache(), b.op("list_b").WithCache()
	call(t, r, opA, qIn{"x"})
	call(t, r, opA, qIn{"y"})
	call(t, r, opB, qIn{"x"})
	call(t, r, opB, qIn{"x"})
	if a.n != 2 || b.n != 1 {
		t.Fatalf("ran a=%d b=%d, want 2 and 1", a.n, b.n)
	}
}

func TestInvokeDoesNotCacheUnmarkedOrErrors(t *testing.T) {
	r, _ := testReg()
	plain, failing := &counted{}, &counted{err: api.NotFound("thing")}
	opP, opF := plain.op("list_p"), failing.op("list_f").WithCache()
	for i := 0; i < 2; i++ {
		call(t, r, opP, qIn{"x"})
		call(t, r, opF, qIn{"x"})
	}
	if plain.n != 2 || failing.n != 2 {
		t.Fatalf("ran plain=%d failing=%d, want 2 each", plain.n, failing.n)
	}
}

func TestInvalidateDropsCache(t *testing.T) {
	r, _ := testReg()
	c := &counted{}
	op := c.op("list_a").WithCache()
	call(t, r, op, qIn{"x"})
	r.Invalidate()
	call(t, r, op, qIn{"x"})
	if c.n != 2 {
		t.Fatalf("ran %d times, want 2", c.n)
	}
}

func TestInvalidateDuringCallIsNotCached(t *testing.T) {
	r, _ := testReg()
	c := &counted{}
	c.fn = func() {
		if c.n == 1 {
			r.Invalidate()
		}
	}
	op := c.op("list_a").WithCache()
	call(t, r, op, qIn{"x"})
	call(t, r, op, qIn{"x"})
	if c.n != 2 {
		t.Fatalf("ran %d times, want 2: a pre-invalidation result was cached", c.n)
	}
}

func TestCacheIsBounded(t *testing.T) {
	r, _ := testReg()
	c := &counted{}
	op := c.op("list_a").WithCache()
	for i := 0; i <= MaxCacheEntries; i++ {
		call(t, r, op, qIn{fmt.Sprint(i)})
	}
	if n := r.cache.len(); n > MaxCacheEntries {
		t.Fatalf("cache holds %d entries, cap %d", n, MaxCacheEntries)
	}
}

func TestInvokeLogsEachCall(t *testing.T) {
	r, _ := testReg()
	var buf bytes.Buffer
	r.SetLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	ok, bad := &counted{}, &counted{err: api.NotFound("thing")}
	long := strings.Repeat("é", 300)
	opOK, opBad := ok.op("search_a").WithCache(), bad.op("get_b")
	call(t, r, opOK, qIn{long})
	call(t, r, opOK, qIn{long})
	call(t, r, opBad, qIn{"z"})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d records, want 3:\n%s", len(lines), buf.String())
	}
	base := []string{"cached", "latency_ms", "level", "msg", "params", "time", "tool"}
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		want := append([]string(nil), base...)
		if i == 2 {
			want = append(want, "error")
		}
		sort.Strings(want)
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Errorf("record %d keys %v, want %v", i, keys, want)
		}
		if m["msg"] != "public api call" {
			t.Errorf("record %d msg %v", i, m["msg"])
		}
		if _, ok := m["latency_ms"].(float64); !ok {
			t.Errorf("record %d latency_ms %v not numeric", i, m["latency_ms"])
		}
		if want := i == 1; m["cached"] != want {
			t.Errorf("record %d cached %v, want %v", i, m["cached"], want)
		}
		if i < 2 {
			var p qIn
			if err := json.Unmarshal([]byte(m["params"].(string)), &p); err != nil {
				t.Fatal(err)
			}
			if p.Query != strings.Repeat("é", LogQueryRunes)+"…" {
				t.Errorf("record %d logged query %q", i, p.Query)
			}
			if m["tool"] != "search_a" {
				t.Errorf("record %d tool %v", i, m["tool"])
			}
		}
	}
	var last map[string]any
	_ = json.Unmarshal([]byte(lines[2]), &last)
	if last["error"] != "not_found" {
		t.Errorf("error %v, want not_found", last["error"])
	}
}
