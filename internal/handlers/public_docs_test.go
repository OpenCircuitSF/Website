package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicmcp"
	"github.com/google/jsonschema-go/jsonschema"
)

// publicDocsPage is the slice of web/src/content/docs/mcp.json and api.json
// these guards read (#0735).
type publicDocsPage struct {
	Sections []struct {
		Heading string `json:"heading"`
		Blocks  []struct {
			Type  string   `json:"type"`
			Label string   `json:"label"`
			Text  string   `json:"text"`
			Items []string `json:"items"`
		} `json:"blocks"`
	} `json:"sections"`
}

const publicDocsMCPFile = "../../web/src/content/docs/mcp.json"

// docsToolDrift compares the Tools section's first list block with ops, in
// order, as "name — Summary". It returns one line per difference.
func docsToolDrift(raw []byte, ops []publicapi.Op) []string {
	var page publicDocsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return []string{"decode: " + err.Error()}
	}
	var items []string
	found := false
	for _, s := range page.Sections {
		if s.Heading != "Tools" {
			continue
		}
		for _, b := range s.Blocks {
			if b.Type == "list" {
				items, found = b.Items, true
				break
			}
		}
	}
	if !found {
		return []string{"no list block in a Tools section"}
	}
	var drift []string
	for i := 0; i < len(ops) || i < len(items); i++ {
		want, got := "", ""
		if i < len(ops) {
			want = ops[i].Name + " — " + ops[i].Summary
		}
		if i < len(items) {
			got = items[i]
		}
		if want != got {
			drift = append(drift, fmt.Sprintf("tool %d: docs %q, registry %q", i, got, want))
		}
	}
	return drift
}

func TestPublicDocsMCPPageMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile(publicDocsMCPFile)
	if err != nil {
		t.Fatal(err)
	}
	ops := BuildPublicRegistry(PublicDeps{}).Ops()
	if len(ops) < 6 {
		t.Fatalf("registry has %d ops, want at least 6", len(ops))
	}
	for _, d := range docsToolDrift(raw, ops) {
		t.Error(d)
	}

	endpoint := "https://www.opencircuitsf.com" + publicmcp.Path
	var page publicDocsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	var sawCommand, sawJSON bool
	for _, s := range page.Sections {
		for _, b := range s.Blocks {
			if b.Type != "code" {
				continue
			}
			if b.Text == "claude mcp add --transport http opencircuitsf "+endpoint {
				sawCommand = true
			}
			if b.Label == "mcpServers JSON" {
				sawJSON = true
				var cfg struct {
					MCPServers map[string]struct {
						Type string `json:"type"`
						URL  string `json:"url"`
					} `json:"mcpServers"`
				}
				if !json.Valid([]byte(b.Text)) || json.Unmarshal([]byte(b.Text), &cfg) != nil {
					t.Errorf("mcpServers JSON block is not valid JSON")
				} else if got := cfg.MCPServers["opencircuitsf"].URL; got != endpoint {
					t.Errorf("mcpServers.opencircuitsf.url = %q, want %q", got, endpoint)
				}
				if got := cfg.MCPServers["opencircuitsf"].Type; got != "http" {
					t.Errorf("mcpServers.opencircuitsf.type = %q, want \"http\"", got)
				}
			}
		}
	}
	if !sawCommand {
		t.Errorf("no code block holds the claude mcp add command for %s", endpoint)
	}
	if !sawJSON {
		t.Errorf("no code block labelled mcpServers JSON")
	}
}

// TestDocsToolDriftBites proves the comparison fails when a tool is missing
// from the docs.
func TestDocsToolDriftBites(t *testing.T) {
	raw, err := os.ReadFile(publicDocsMCPFile)
	if err != nil {
		t.Fatal(err)
	}
	var page map[string]any
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	mutated := false
	for _, s := range page["sections"].([]any) {
		sec := s.(map[string]any)
		if sec["heading"] != "Tools" {
			continue
		}
		for _, b := range sec["blocks"].([]any) {
			blk := b.(map[string]any)
			if blk["type"] == "list" && !mutated {
				items := blk["items"].([]any)
				blk["items"] = items[:len(items)-1]
				mutated = true
			}
		}
	}
	if !mutated {
		t.Fatal("no Tools list block to mutate")
	}
	out, _ := json.Marshal(page)
	if len(docsToolDrift(out, BuildPublicRegistry(PublicDeps{}).Ops())) == 0 {
		t.Fatal("docsToolDrift reported nothing for a page missing a tool")
	}
}

const publicDocsAPIFile = "../../web/src/content/docs/api.json"

// jsonPaths adds the path of every object key in v to into: an object key
// k under prefix records prefix+"."+k, and an array's elements sit under
// prefix+"[]" (#0737).
func jsonPaths(v any, prefix string, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			into[prefix+"."+k] = true
			jsonPaths(e, prefix+"."+k, into)
		}
	case []any:
		for _, e := range x {
			jsonPaths(e, prefix+"[]", into)
		}
	}
}

// schemaPaths is jsonPaths over a schema: Properties as object keys and
// Items as array elements. A $ref or $defs is recorded as an unmatchable
// path, so it shows up as drift rather than being followed (#0737).
func schemaPaths(s *jsonschema.Schema, prefix string, into map[string]bool) {
	if s == nil {
		return
	}
	if s.Ref != "" || len(s.Defs) > 0 || len(s.Definitions) > 0 {
		into[prefix+" ($ref/$defs not followed)"] = true
	}
	for k, p := range s.Properties {
		into[prefix+"."+k] = true
		schemaPaths(p, prefix+"."+k, into)
	}
	schemaPaths(s.Items, prefix+"[]", into)
}

// docsParamNames returns the parameter names an op section's prose lists
// after "Parameters: " (or "Parameter: "), nil when it lists none. Clauses
// split on ";" at parenthesis depth 0; each clause's names run up to its
// first "(" or ",", and "a and b" names two (#0737).
func docsParamNames(text string) []string {
	i := strings.Index(text, "Parameters: ")
	n := len("Parameters: ")
	if i < 0 {
		i = strings.Index(text, "Parameter: ")
		n = len("Parameter: ")
	}
	if i < 0 {
		return nil
	}
	rest := text[i+n:]
	var clauses []string
	depth, start := 0, 0
	for k, r := range rest {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ';':
			if depth == 0 {
				clauses = append(clauses, rest[start:k])
				start = k + 1
			}
		}
	}
	clauses = append(clauses, rest[start:])
	var out []string
	for _, c := range clauses {
		if k := strings.IndexAny(c, "(,"); k >= 0 {
			c = c[:k]
		}
		c = strings.TrimSuffix(strings.TrimSpace(c), ".")
		for _, name := range strings.Split(c, " and ") {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// docsAPIDrift checks /docs/api's per-endpoint sections against ops: one
// "GET <path>" section per op and no other, an Example request curl whose
// URL matches the op's path, prose whose "Parameters:" list names exactly
// the op's query parameters, and an Example response whose top-level keys
// equal the output schema's properties and whose key paths (#0737) equal
// the schema's. It returns one line per difference.
func docsAPIDrift(raw []byte, ops []publicapi.Op) []string {
	var page publicDocsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return []string{"decode: " + err.Error()}
	}
	const site = "https://www.opencircuitsf.com"
	var drift []string
	seen := map[string]bool{}
	byHeading := map[string]int{}
	for i, s := range page.Sections {
		if strings.HasPrefix(s.Heading, "GET "+publicapi.Prefix) {
			byHeading[s.Heading] = i
		}
	}
	for _, op := range ops {
		h := "GET " + op.Path
		seen[h] = true
		i, ok := byHeading[h]
		if !ok {
			drift = append(drift, "no section "+h)
			continue
		}
		var wantParams []string
		for _, u := range op.URLParams() {
			if !u.InPath {
				wantParams = append(wantParams, u.Name)
			}
		}
		var gotParams []string
		hasProse := false
		for _, b := range page.Sections[i].Blocks {
			if b.Type == "p" {
				gotParams, hasProse = docsParamNames(b.Text), true
				break
			}
		}
		slices.Sort(wantParams)
		slices.Sort(gotParams)
		if !hasProse && len(wantParams) > 0 {
			drift = append(drift, fmt.Sprintf("%s: no prose block, input schema %v", h, wantParams))
		} else if !slices.Equal(gotParams, wantParams) {
			drift = append(drift, fmt.Sprintf("%s: parameters %v, input schema %v", h, gotParams, wantParams))
		}
		var codes []string
		var labels []string
		for _, b := range page.Sections[i].Blocks {
			if b.Type == "code" {
				codes = append(codes, b.Text)
				labels = append(labels, b.Label)
			}
		}
		if len(codes) < 2 || labels[0] != "Example request" || labels[1] != "Example response" {
			drift = append(drift, h+": want an Example request and an Example response code block")
			continue
		}
		pathRE := regexp.MustCompile(`\\\{[^}]+\\\}`).ReplaceAllString(regexp.QuoteMeta(op.Path), `[^/?']+`)
		reqRE := regexp.MustCompile(`^curl -s '` + regexp.QuoteMeta(site) + pathRE + `(\?[^']*)?'$`)
		if !reqRE.MatchString(codes[0]) {
			drift = append(drift, fmt.Sprintf("%s: request %q does not match the path", h, codes[0]))
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(codes[1]), &resp); err != nil {
			drift = append(drift, h+": response is not a JSON object: "+err.Error())
			continue
		}
		schema := op.OutputSchema()
		top, wantTop := map[string]bool{}, map[string]bool{}
		for k := range resp {
			top[k] = true
		}
		for k := range schema.Properties {
			wantTop[k] = true
		}
		if g, w := sortedKeys(top), sortedKeys(wantTop); !slices.Equal(g, w) {
			drift = append(drift, fmt.Sprintf("%s: response keys %v, schema %v", h, g, w))
		}
		all, wantAll := map[string]bool{}, map[string]bool{}
		jsonPaths(resp, "", all)
		schemaPaths(schema, "", wantAll)
		if g, w := sortedKeys(all), sortedKeys(wantAll); !slices.Equal(g, w) {
			drift = append(drift, fmt.Sprintf("%s: response key paths %v, schema %v", h, g, w))
		}
	}
	for h := range byHeading {
		if !seen[h] {
			drift = append(drift, "section "+h+" names no registry operation")
		}
	}
	return drift
}

func TestPublicDocsAPIPageMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile(publicDocsAPIFile)
	if err != nil {
		t.Fatal(err)
	}
	ops := BuildPublicRegistry(PublicDeps{}).Ops()
	if len(ops) < 6 {
		t.Fatalf("registry has %d ops, want at least 6", len(ops))
	}
	for _, d := range docsAPIDrift(raw, ops) {
		t.Error(d)
	}
	text := string(raw)
	for _, want := range []string{
		"https://www.opencircuitsf.com" + publicapi.Prefix,
		"https://www.opencircuitsf.com" + publicapi.OpenAPIPath,
		"limit defaults to " + strconv.Itoa(publicapi.DefaultLimit) + " and is capped at " + strconv.Itoa(publicapi.MaxLimit),
		"cut to " + strconv.Itoa(publicapi.MaxQueryRunes) + " characters",
		strconv.Itoa(publicapi.RequestsPerMinute) + " requests a minute per IP",
		"up to " + strconv.Itoa(int(publicapi.ListCacheTTL.Seconds())) + " seconds",
		"Retry-After",
		"/docs/mcp",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("api.json does not contain %q", want)
		}
	}
	mcp, err := os.ReadFile(publicDocsMCPFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := strconv.Itoa(publicapi.RequestsPerMinute) + " requests a minute per IP"; !strings.Contains(string(mcp), want) {
		t.Errorf("mcp.json does not contain %q", want)
	}
}

// TestDocsAPIDriftBites proves the comparison fails on a renamed response
// key, a dropped section and a request for the wrong path.
func TestDocsAPIDriftBites(t *testing.T) {
	raw, err := os.ReadFile(publicDocsAPIFile)
	if err != nil {
		t.Fatal(err)
	}
	ops := BuildPublicRegistry(PublicDeps{}).Ops()
	for name, mutate := range map[string]func(string) string{
		"renamed key": func(s string) string { return strings.Replace(s, `\"starts_at\"`, `\"start\"`, 1) },
		"dropped field": func(s string) string {
			return strings.Replace(s, `\"location_note\": \"Ring the buzzer.\",\n  `, "", 1)
		},
		"wrong path":     func(s string) string { return strings.Replace(s, "/api/public/v1/about'", "/api/public/v1/abouts'", 1) },
		"dropped header": func(s string) string { return strings.Replace(s, `"GET /api/public/v1/newsletter"`, `"Newsletter"`, 1) },
		"extra param": func(s string) string {
			return strings.Replace(s, "Parameter: limit (default 10, max 50).", "Parameter: limit (default 10, max 50); offset (default 0).", 1)
		},
		"missing param": func(s string) string { return strings.Replace(s, "; after and before, each", "; after, each", 1) },
		// #0737: id already exists at events[].id, so only the per-path
		// check sees the new .events[].tags[].id.
		"nested key path": func(s string) string {
			return strings.Replace(s, `\"microcontrollers\"`, `{\"id\": \"microcontrollers\"}`, 1)
		},
	} {
		mutated := mutate(string(raw))
		if mutated == string(raw) {
			t.Errorf("%s: mutation did not apply", name)
			continue
		}
		if len(docsAPIDrift([]byte(mutated), ops)) == 0 {
			t.Errorf("%s: docsAPIDrift reported nothing", name)
		}
	}
}

// TestPublicDocsNameDiscoveryURLs: /docs/feeds and /docs/mcp each show the
// server card and AI catalog URLs as code blocks (#0737).
func TestPublicDocsNameDiscoveryURLs(t *testing.T) {
	const site = "https://www.opencircuitsf.com"
	for _, f := range []string{"feeds.json", "mcp.json"} {
		raw, err := os.ReadFile("../../web/src/content/docs/" + f)
		if err != nil {
			t.Fatal(err)
		}
		var page publicDocsPage
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		codes := map[string]bool{}
		for _, s := range page.Sections {
			for _, b := range s.Blocks {
				if b.Type == "code" {
					codes[b.Text] = true
				}
			}
		}
		for _, u := range []string{site + publicmcp.CardPath, site + publicmcp.CatalogPath} {
			if !codes[u] {
				t.Errorf("%s: no code block showing %s", f, u)
			}
		}
	}
}
