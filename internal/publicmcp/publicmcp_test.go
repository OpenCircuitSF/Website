package publicmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
)

type stubIn struct {
	Slug string `json:"slug" path:"slug"`
}

type stubOut struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type stubListIn struct {
	Limit *int `json:"limit,omitempty" query:"limit"`
}

type stubList struct {
	Items []stubOut `json:"items"`
}

// stubRegistry is two public ops: a list and a get that knows one slug.
func stubRegistry() *publicapi.Registry {
	reg := publicapi.NewRegistry("test")
	reg.Add(
		publicapi.Define(api.Operation{Name: "list_things", Method: http.MethodGet, Path: publicapi.Prefix + "things", Summary: "List things."},
			func(_ context.Context, in stubListIn) (stubList, error) {
				return stubList{Items: []stubOut{{Slug: "a", Title: "A"}}}, nil
			}, func(out stubList) string { return "1 thing." }),
		publicapi.Define(api.Operation{Name: "get_thing", Method: http.MethodGet, Path: publicapi.Prefix + "things/{slug}", Summary: "Get a thing."},
			func(_ context.Context, in stubIn) (stubOut, error) {
				if in.Slug != "a" {
					return stubOut{}, api.NotFound("thing")
				}
				return stubOut{Slug: "a", Title: "A"}, nil
			}, func(out stubOut) string { return out.Title + " — thing" }),
	)
	return reg
}

func serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(NewHandler(stubRegistry(), nil))
	t.Cleanup(srv.Close)
	return srv.URL
}

func connect(t *testing.T, url string, tr *mcptest.Transport) *mcp.ClientSession {
	t.Helper()
	return mcptest.ConnectURL(t, url, tr)
}

func TestToolsListIsExactlyTheRegistry(t *testing.T) {
	cs := connect(t, serve(t), &mcptest.Transport{})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		a := tl.Annotations
		if a == nil || !a.ReadOnlyHint || !a.IdempotentHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s annotations = %+v", tl.Name, a)
		}
		if tl.OutputSchema == nil {
			t.Errorf("%s has no output schema", tl.Name)
		}
		if !strings.Contains(tl.Description, "no account or key needed") {
			t.Errorf("%s description = %q", tl.Name, tl.Description)
		}
	}
	slices.Sort(names)
	if want := []string{"get_thing", "list_things"}; !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestCallReturnsStructuredContentAndOneLineSummary(t *testing.T) {
	cs := connect(t, serve(t), &mcptest.Transport{})
	res := mcptest.Call(t, cs, "get_thing", map[string]any{"slug": "a"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("result = %+v", res)
	}
	sum := res.Content[0].(*mcp.TextContent).Text
	if sum != "A — thing" || strings.Contains(sum, "\n") {
		t.Errorf("summary = %q", sum)
	}
	var fromText, fromStruct stubOut
	if err := json.Unmarshal([]byte(res.Content[1].(*mcp.TextContent).Text), &fromText); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(b, &fromStruct); err != nil {
		t.Fatal(err)
	}
	want := stubOut{Slug: "a", Title: "A"}
	if fromText != want || fromStruct != want {
		t.Errorf("text %+v, structured %+v, want %+v", fromText, fromStruct, want)
	}
}

func TestNotFoundIsAToolError(t *testing.T) {
	cs := connect(t, serve(t), &mcptest.Transport{})
	res := mcptest.Call(t, cs, "get_thing", map[string]any{"slug": "nope"})
	if e := mcptest.ErrorOf(t, res); e.Code != api.CodeNotFound {
		t.Errorf("code = %q", e.Code)
	}
	if res.StructuredContent != nil {
		t.Errorf("structured content on an error: %v", res.StructuredContent)
	}
}

func TestAuthorizationIsIgnored(t *testing.T) {
	url := serve(t)
	run := func(bearer string) (any, any) {
		cs := connect(t, url, &mcptest.Transport{Bearer: bearer})
		tools, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatalf("bearer %q: list: %v", bearer, err)
		}
		res := mcptest.Call(t, cs, "get_thing", map[string]any{"slug": "a"})
		return tools.Tools, res.Content
	}
	t1, c1 := run("")
	t2, c2 := run("ocsf_bogus")
	j := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	if j(t1) != j(t2) || !reflect.DeepEqual(j(c1), j(c2)) {
		t.Errorf("results differ with an Authorization header")
	}
	const init = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(init))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer ocsf_bogus")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("initialize with bogus bearer = %d, WWW-Authenticate %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestProxiedHostAccepted(t *testing.T) {
	cs, err := mcptest.Dial(serve(t), &mcptest.Transport{Host: "www.opencircuitsf.com"})
	if err != nil {
		t.Fatalf("connect with Host www.opencircuitsf.com: %v", err)
	}
	defer cs.Close()
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestGetAndDeleteAreMethodNotAllowed(t *testing.T) {
	url := serve(t)
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req, _ := http.NewRequest(m, url, nil)
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", m, resp.StatusCode)
		}
	}
}

// TestMountRegistersOnlyMCPPath: Mount answers on /mcp for every
// Streamable HTTP method and never on /admin/mcp or a subpath.
func TestMountRegistersOnlyMCPPath(t *testing.T) {
	if Path != "/mcp" {
		t.Fatalf("Path = %q", Path)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	Mount(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }))
	for _, c := range []struct {
		method, path string
		reach        bool
	}{
		{http.MethodPost, "/mcp", true},
		{http.MethodGet, "/mcp", true},
		{http.MethodDelete, "/mcp", true},
		{http.MethodPost, "/admin/mcp", false},
		{http.MethodGet, "/admin/mcp", false},
		{http.MethodPost, "/mcp/x", false},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if (rec.Code == 299) != c.reach {
			t.Errorf("%s %s = %d, reach want %v", c.method, c.path, rec.Code, c.reach)
		}
	}
}
