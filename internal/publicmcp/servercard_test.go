package publicmcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
)

const testBase = "https://www.opencircuitsf.com"

func decodeCard(t *testing.T, b []byte) serverCard {
	t.Helper()
	var c serverCard
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("decode card: %v\n%s", err, b)
	}
	return c
}

// hasKey reports whether key appears as an object key anywhere in v.
func hasKey(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if k == key || hasKey(e, key) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if hasKey(e, key) {
				return true
			}
		}
	}
	return false
}

// TestServerCardMatchesSchemaV1 pins the v1 schema's constraints (schema.ts
// at 28db12c9) on the card, and that it lists no tools or auth headers.
func TestServerCardMatchesSchemaV1(t *testing.T) {
	b, err := BuildServerCard("1.2.3", testBase+"/")
	if err != nil {
		t.Fatal(err)
	}
	c := decodeCard(t, b)
	if c.Schema != CardSchema {
		t.Errorf("$schema = %q", c.Schema)
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9.-]+/[a-zA-Z0-9._-]+$`).MatchString(c.Name) || len(c.Name) < 3 || len(c.Name) > 200 {
		t.Errorf("name = %q breaks the schema pattern", c.Name)
	}
	if n := utf8.RuneCountInString(c.Description); n < 1 || n > 100 {
		t.Errorf("description is %d runes, want 1-100", n)
	}
	if utf8.RuneCountInString(c.Title) > 100 {
		t.Errorf("title too long: %q", c.Title)
	}
	if c.Version != "1.2.3" {
		t.Errorf("version = %q", c.Version)
	}
	if len(c.Remotes) != 1 || c.Remotes[0].Type != "streamable-http" || c.Remotes[0].URL != testBase+"/mcp" {
		t.Fatalf("remotes = %+v", c.Remotes)
	}
	if !reflect.DeepEqual(c.Remotes[0].SupportedProtocolVersions, mcp.SupportedProtocolVersions()) {
		t.Errorf("supportedProtocolVersions = %v", c.Remotes[0].SupportedProtocolVersions)
	}
	if !strings.HasSuffix(c.WebsiteURL, "/docs/mcp") {
		t.Errorf("websiteUrl = %q", c.WebsiteURL)
	}
	var raw any
	_ = json.Unmarshal(b, &raw)
	for _, k := range []string{"tools", "capabilities", "headers"} {
		if hasKey(raw, k) {
			t.Errorf("card has a %q key; a v1 card carries none", k)
		}
	}
}

// TestServerCardConsistentWithLiveServer: the card's version and title are
// what the live server says on initialize.
func TestServerCardConsistentWithLiveServer(t *testing.T) {
	reg := publicapi.NewRegistry("9.9.9")
	reg.Add(stubRegistry().Ops()...)
	srv := httptest.NewServer(NewHandler(reg, nil, nil))
	t.Cleanup(srv.Close)
	cs := mcptest.ConnectURL(t, srv.URL, &mcptest.Transport{})
	info := cs.InitializeResult().ServerInfo
	b, err := BuildServerCard(reg.Version(), testBase)
	if err != nil {
		t.Fatal(err)
	}
	c := decodeCard(t, b)
	if info.Version != c.Version || c.Version != "9.9.9" {
		t.Errorf("live version %q, card %q", info.Version, c.Version)
	}
	if info.Title != c.Title {
		t.Errorf("live title %q, card %q", info.Title, c.Title)
	}
	if !strings.HasSuffix(c.Name, "/"+info.Name) || info.Name != ServerName {
		t.Errorf("card name %q, live name %q", c.Name, info.Name)
	}
}

func TestCatalogPointsAtCard(t *testing.T) {
	b, err := buildCatalog(testBase + "/")
	if err != nil {
		t.Fatal(err)
	}
	var c catalog
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if c.SpecVersion != "1.0" || len(c.Entries) != 1 {
		t.Fatalf("catalog = %s", b)
	}
	e := c.Entries[0]
	if e.Type != CardMediaType || e.URL != testBase+CardPath || !strings.HasPrefix(e.Identifier, "urn:air:opencircuitsf.com:") {
		t.Errorf("entry = %+v", e)
	}
}

func discoveryMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	MountDiscovery(mux, stubRegistry(), testBase)
	return mux
}

func TestDiscoveryHTTP(t *testing.T) {
	mux := discoveryMux(t)
	do := func(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	bodies := map[string][]byte{}
	for path, ct := range map[string]string{CardPath: CardMediaType, WellKnownCardAlias: CardMediaType, CatalogPath: CatalogMediaType} {
		rec := do(http.MethodGet, path, nil)
		h := rec.Header()
		if rec.Code != 200 || h.Get("Content-Type") != ct || h.Get("Access-Control-Allow-Origin") != "*" ||
			h.Get("Cache-Control") != "public, max-age=3600" || h.Get("ETag") == "" ||
			h.Get("Access-Control-Expose-Headers") != "ETag" {
			t.Errorf("GET %s = %d %v", path, rec.Code, h)
		}
		bodies[path], _ = io.ReadAll(rec.Body)
		nm := do(http.MethodGet, path, map[string]string{"If-None-Match": h.Get("ETag")})
		if nm.Code != http.StatusNotModified || nm.Body.Len() != 0 {
			t.Errorf("conditional GET %s = %d, %d bytes", path, nm.Code, nm.Body.Len())
		}
		op := do(http.MethodOptions, path, nil)
		if op.Code != http.StatusNoContent || op.Header().Get("Access-Control-Allow-Methods") != "GET" ||
			op.Header().Get("Access-Control-Allow-Headers") != "Content-Type, If-None-Match" {
			t.Errorf("OPTIONS %s = %d %v", path, op.Code, op.Header())
		}
	}
	if !bytes.Equal(bodies[CardPath], bodies[WellKnownCardAlias]) {
		t.Error("card and alias bodies differ")
	}
	if rec := do(http.MethodGet, "/.well-known/atproto-did", nil); rec.Code != http.StatusNotFound {
		t.Errorf("atproto-did = %d; only the exact discovery paths may be registered", rec.Code)
	}
}

// TestBrowserGetRedirectsToDocs: a browser GET of /mcp is a 302 to the docs
// page; an MCP client's GET still reaches the SDK (405), and POST is untouched.
func TestBrowserGetRedirectsToDocs(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, NewHandler(stubRegistry(), nil, nil))
	get := func(accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, Path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := get("text/html,application/xhtml+xml"); rec.Code != http.StatusFound || rec.Header().Get("Location") != DocsPath {
		t.Errorf("browser GET = %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	// "text/html, text/event-stream" is what pins wantsHTML's event-stream
	// exclusion: without it every case here lacks text/html (#0731 review).
	for _, a := range []string{"text/event-stream", "application/json, text/event-stream", "", "text/html, text/event-stream"} {
		if rec := get(a); rec.Code == http.StatusFound || rec.Code/100 == 3 {
			t.Errorf("GET Accept %q redirected (%d)", a, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	// Accept is text/html alone, so a POST wrapped in browserToDocs would
	// redirect; unwrapped, the SDK rejects it without a 3xx.
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code/100 == 3 {
		t.Errorf("POST redirected (%d)", rec.Code)
	}
}
