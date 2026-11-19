package seo

// #0520: the full About and Privacy page text in raw server HTML, from ONE
// shared source. web/src/content/about.json and web/src/content/privacy.json
// are that source: the SPA's About.svelte / PrivacyPolicy.svelte parse them
// through web/src/lib/staticPage.ts, and this file reads the same embedded
// bytes (web.StaticContent) at package init and renders the same text into
// tokenBody. Neither side keeps a hand-typed second copy, so the two
// renderings cannot drift -- the arrangement #0519 used for these two
// routes (staticPageCopy entries plus the fallback_copy_guard_test.go drift
// guard) existed only until this single-source file existed, and those two
// entries were removed from staticPageCopy with it.
//
// The two runtimes each interpret the JSON; the STRUCTURE they agree on is
// small and pinned by tests on both sides (internal/seo/staticpages_test.go
// renders every h1/heading/block from the embedded JSON into the served
// HTML; web/src/lib/staticPage.test.ts parses the same files and
// web/src/lib/pageTitle.guard.test.ts pins the h1s). A malformed JSON fails
// loudly at init (mustLoadStaticPages) rather than serving a half page.
//
// #0735: the website's Docs section uses the same arrangement. Every
// web/src/content/docs/*.json file is one docs page (index.json is /docs,
// any other stem is /docs/<stem>), rendered by web/src/views/Docs.svelte on
// the SPA side and by this file's loader on the server side. Docs pages must
// also carry a title and description, which seo.go and sitemap.go read so a
// new docs page needs no hand-written meta entry.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/web"
)

// staticPageData is one content JSON file's decoded, link-resolved shape.
// Field-for-field the twin of web/src/lib/staticPage.ts's
// StaticPageContent -- including each block's KIND, which stays a small
// string tag ("p", "list", "links") rather than a Go enum so the JSON
// round-trips through both parsers with one spelling for each kind.
type staticPageData struct {
	H1          string            `json:"h1"`
	Title       string            `json:"title"`       // #0735: required on docs pages, optional elsewhere
	Description string            `json:"description"` // #0735: required on docs pages, optional elsewhere
	Intro       []staticBlock     `json:"intro"`
	Sections    []staticSection   `json:"sections"`
	Links       map[string]string `json:"links"`
}

type staticSection struct {
	Heading string        `json:"heading"`
	Blocks  []staticBlock `json:"blocks"`
}

type contentLink struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Href is resolved from the page's links map at load time (it is not
	// part of the JSON itself), so pageTemplate never needs the map -- the
	// template stays as dumb as every other branch in pageTemplateSource.
	Href string `json:"-"`
	// External is set at load time (#0735): true for an off-site href, so
	// the fallback opens it in a new tab while an internal /path link stays
	// a plain same-tab anchor, matching staticPage.ts's externalLinkAttrs.
	External bool `json:"-"`
}

// staticBlock is one decoded content block, AFTER link resolution.
type staticBlock struct {
	Type   string // "p", "list", "links", or "code" -- the JSON's own discriminator
	Text   string
	Label  string // code block's accessible name (#0735)
	Strong string // optional leading <strong> label ("Unsubscribe.")
	Muted  bool   // optional muted paragraph (class="text-muted", styled by the SPA stylesheet the server HTML already loads)
	Tail   string // optional text placed after the inline link (".", typically)
	Link   contentLink
	Items  []string      // list block
	Links  []contentLink // links block (one external anchor per item)
}

// UnmarshalJSON handles the JSON's one polymorphic field: a block's "items"
// is a string array for a list block and a {key,label} array for a links
// block, which a single struct field cannot carry. Everything else decodes
// positionally.
func (b *staticBlock) UnmarshalJSON(data []byte) error {
	var aux struct {
		Type   string          `json:"type"`
		Text   string          `json:"text"`
		Label  string          `json:"label"`
		Strong string          `json:"strong"`
		Muted  bool            `json:"muted"`
		Tail   string          `json:"tail"`
		Link   contentLink     `json:"link"`
		Items  json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	b.Type, b.Text, b.Label, b.Strong, b.Muted, b.Tail, b.Link = aux.Type, aux.Text, aux.Label, aux.Strong, aux.Muted, aux.Tail, aux.Link
	switch b.Type {
	case "list":
		return json.Unmarshal(aux.Items, &b.Items)
	case "links":
		return json.Unmarshal(aux.Items, &b.Links)
	}
	return nil
}

// resolveBlocks validates one block list and resolves every link key
// against the page's links map. A missing key or an unknown block type is
// a startup panic (a programmer error in committed, compile-time-embedded
// data), never a silent degradation.
func resolveBlocks(blocks []staticBlock, links map[string]string, where string) error {
	for i := range blocks {
		b := &blocks[i]
		switch b.Type {
		case "p":
			if b.Text == "" {
				return fmt.Errorf("%s block %d is a p block without text", where, i)
			}
			if b.Link.Label != "" {
				href, ok := links[b.Link.Key]
				if !ok || href == "" {
					return fmt.Errorf("%s block %d references unknown link key %q", where, i, b.Link.Key)
				}
				b.Link.Href = href
				b.Link.External = isExternalHref(href)
			}
		case "list":
			if len(b.Items) == 0 {
				return fmt.Errorf("%s block %d is a list block without items", where, i)
			}
		case "links":
			if len(b.Links) == 0 {
				return fmt.Errorf("%s block %d is a links block without items", where, i)
			}
			for j := range b.Links {
				href, ok := links[b.Links[j].Key]
				if !ok || href == "" {
					return fmt.Errorf("%s block %d references unknown link key %q", where, i, b.Links[j].Key)
				}
				b.Links[j].Href = href
				b.Links[j].External = isExternalHref(href)
			}
		case "code":
			if b.Text == "" || b.Label == "" {
				return fmt.Errorf("%s block %d is a code block without text or label", where, i)
			}
		default:
			return fmt.Errorf("%s block %d has unknown type %q", where, i, b.Type)
		}
	}
	return nil
}

// isExternalHref reports whether a resolved href leaves the site (#0735).
func isExternalHref(href string) bool {
	return !strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "mailto:")
}

// loadStaticPage decodes and validates one content file, resolving every
// block's link key against the page's links map at load time.
func loadStaticPage(raw []byte) (staticPageData, error) {
	var data staticPageData
	if err := json.Unmarshal(raw, &data); err != nil {
		return staticPageData{}, err
	}
	if data.H1 == "" || len(data.Intro) == 0 || len(data.Sections) == 0 || data.Links == nil {
		return staticPageData{}, fmt.Errorf("missing h1, intro, sections, or links")
	}
	if err := resolveBlocks(data.Intro, data.Links, "intro"); err != nil {
		return staticPageData{}, err
	}
	for i := range data.Sections {
		if data.Sections[i].Heading == "" || len(data.Sections[i].Blocks) == 0 {
			return staticPageData{}, fmt.Errorf("section %d is missing a heading or blocks", i)
		}
		if err := resolveBlocks(data.Sections[i].Blocks, data.Links, fmt.Sprintf("section %d", i)); err != nil {
			return staticPageData{}, err
		}
	}
	return data, nil
}

// mustLoadStaticPages reads every *.json in the embedded static content
// directory and indexes the decoded pages by their content filename's path
// key: about.json -> "/about", privacy.json -> "/privacy". The mapping is
// filename-derived so a page can never be embedded without also becoming
// servable (or vice versa) -- the file IS the route's source.
func mustLoadStaticPages(content fs.FS) map[string]staticPageData {
	entries, err := fs.ReadDir(content, ".")
	if err != nil {
		panic(fmt.Sprintf("seo: reading embedded static page content: %v", err))
	}
	pages := make(map[string]staticPageData, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(content, e.Name())
		if err != nil {
			panic(fmt.Sprintf("seo: reading embedded static page content %s: %v", e.Name(), err))
		}
		data, err := loadStaticPage(raw)
		if err != nil {
			panic(fmt.Sprintf("seo: embedded static page content %s: %v", e.Name(), err))
		}
		pages["/"+strings.TrimSuffix(e.Name(), ".json")] = data
	}
	// #0735: the docs subdirectory, one page per file. It is committed
	// content, so a missing directory is a startup panic like a bad file.
	docEntries, err := fs.ReadDir(content, "docs")
	if err != nil {
		panic(fmt.Sprintf("seo: reading embedded docs content: %v", err))
	}
	for _, e := range docEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(content, "docs/"+e.Name())
		if err != nil {
			panic(fmt.Sprintf("seo: reading embedded docs page %s: %v", e.Name(), err))
		}
		data, err := loadStaticPage(raw)
		if err != nil {
			panic(fmt.Sprintf("seo: embedded docs page %s: %v", e.Name(), err))
		}
		key := "/docs"
		if stem := strings.TrimSuffix(e.Name(), ".json"); stem != "index" {
			key = "/docs/" + stem
		}
		if data.Title == "" || data.Description == "" {
			panic(fmt.Sprintf("seo: embedded docs page %s: missing title or description", key))
		}
		pages[key] = data
	}
	if len(pages) == 0 {
		panic("seo: embedded static page content directory is empty -- the shared About/Privacy source is missing from the build")
	}
	return pages
}

// staticPages is the decoded shared source, loaded once at package init.
// The single-source contract means there is deliberately no setter and no
// per-Renderer override: every Renderer renders these two pages from the
// same embedded bytes the SPA bundles.
var staticPages = mustLoadStaticPages(web.StaticContent())

// docsPaths returns every docs page route (#0735), sorted: "/docs" and each
// "/docs/<stem>" loaded from the docs content directory.
func docsPaths() []string {
	var out []string
	for p := range staticPages {
		if p == "/docs" || strings.HasPrefix(p, "/docs/") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
