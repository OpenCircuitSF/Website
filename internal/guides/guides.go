// Package guides is the server's view of the guide catalog (#0656): every
// student workshop guide served at /workshops/{slug} and every topic guide
// served at /guides/{slug}. The catalog is web/src/content/guides/guides.json,
// embedded through web.StaticContent() — the same file the SPA's router and
// views import — so the Go and TypeScript slug sets cannot diverge. It is a
// compile-time constant: nothing here consults the workshop store, so a
// guide route reveals nothing about draft events.
package guides

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/web"
)

// Kind distinguishes the two guide families, which live under different
// URL prefixes.
type Kind int

const (
	// Workshop is a full student guide to an in-person workshop.
	Workshop Kind = iota
	// Topic is a short single-idea explainer.
	Topic
)

// String is the kind's API name: "workshop" or "topic".
func (k Kind) String() string {
	if k == Workshop {
		return "workshop"
	}
	return "topic"
}

// Set is one of the catalog's guide sets (LEDs, ESP32-C3, ...); every guide
// belongs to exactly one.
type Set struct {
	ID    string
	Title string
	Blurb string
}

// Guide is one catalog entry.
type Guide struct {
	Kind  Kind
	Slug  string
	Title string
	// Hook is the one-line teaser on cards and in social posts.
	Hook string
	// Description is the plain-text meta description: the catalog's
	// explicit description when it has one, otherwise Hook.
	Description string
	Minutes     int
	Tags        []string
	// Post is the suggested social-post copy, without the link; topics only.
	Post string
	// Set is the id of the guide's set (see Sets).
	Set string
}

// Path is the guide's public URL path.
func (g Guide) Path() string {
	if g.Kind == Workshop {
		return "/workshops/" + g.Slug
	}
	return "/guides/" + g.Slug
}

type entry struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Hook        string   `json:"hook"`
	Description string   `json:"description"`
	Minutes     int      `json:"minutes"`
	Tags        []string `json:"tags"`
	Post        string   `json:"post"`
	Set         string   `json:"set"`
}

type setEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Blurb string `json:"blurb"`
}

type file struct {
	Sets           []setEntry `json:"sets"`
	WorkshopGuides []entry    `json:"workshopGuides"`
	Topics         []entry    `json:"topics"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// load parses and validates a catalog. Workshop guides come first, then
// topics, each in file order. Every guide must name a declared set.
func load(raw []byte) ([]Guide, []Set, error) {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, nil, fmt.Errorf("guides: parse catalog: %w", err)
	}
	if len(f.WorkshopGuides) == 0 || len(f.Topics) == 0 {
		return nil, nil, errors.New("guides: catalog needs at least one workshop guide and one topic")
	}
	var sets []Set
	setIDs := map[string]bool{}
	for _, se := range f.Sets {
		if !slugPattern.MatchString(se.ID) {
			return nil, nil, fmt.Errorf("guides: set id %q is not URL-safe", se.ID)
		}
		if setIDs[se.ID] {
			return nil, nil, fmt.Errorf("guides: duplicate set id %q", se.ID)
		}
		setIDs[se.ID] = true
		sets = append(sets, Set{ID: se.ID, Title: se.Title, Blurb: se.Blurb})
	}
	seen := map[string]bool{}
	var out []Guide
	add := func(kind Kind, e entry) error {
		if !slugPattern.MatchString(e.Slug) {
			return fmt.Errorf("guides: slug %q is not URL-safe", e.Slug)
		}
		if seen[e.Slug] {
			return fmt.Errorf("guides: duplicate slug %q", e.Slug)
		}
		seen[e.Slug] = true
		if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Hook) == "" {
			return fmt.Errorf("guides: %q needs a title and a hook", e.Slug)
		}
		if kind == Topic && e.Minutes <= 0 {
			return fmt.Errorf("guides: topic %q needs minutes", e.Slug)
		}
		if kind == Topic && (strings.TrimSpace(e.Post) == "" || !isPlainText(e.Post)) {
			return fmt.Errorf("guides: topic %q needs plain post copy", e.Slug)
		}
		if e.Set == "" {
			return fmt.Errorf("guides: %q needs a set", e.Slug)
		}
		if !setIDs[e.Set] {
			return fmt.Errorf("guides: %q names set %q, which is not declared", e.Slug, e.Set)
		}
		if kind == Workshop && e.Post != "" {
			return fmt.Errorf("guides: workshop guide %q must not carry post copy", e.Slug)
		}
		desc := e.Description
		if desc == "" {
			desc = e.Hook
		}
		if !isPlainText(desc) {
			return fmt.Errorf("guides: %q meta description has a control character or soft hyphen; add a plain description", e.Slug)
		}
		out = append(out, Guide{Kind: kind, Slug: e.Slug, Title: e.Title, Hook: e.Hook, Description: desc, Minutes: e.Minutes, Tags: e.Tags, Post: e.Post, Set: e.Set})
		return nil
	}
	for _, e := range f.WorkshopGuides {
		if err := add(Workshop, e); err != nil {
			return nil, nil, err
		}
	}
	for _, e := range f.Topics {
		if err := add(Topic, e); err != nil {
			return nil, nil, err
		}
	}
	return out, sets, nil
}

// isPlainText reports whether s has no C0/C1 control character and no soft
// hyphen (U+00AD), so it is safe as a meta or OG description.
func isPlainText(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0xad {
			return false
		}
	}
	return true
}

func mustLoad(fsys fs.FS) ([]Guide, []Set) {
	raw, err := fs.ReadFile(fsys, "guides/guides.json")
	if err != nil {
		panic("guides: read embedded catalog: " + err.Error())
	}
	gs, sets, err := load(raw)
	if err != nil {
		panic(err.Error())
	}
	return gs, sets
}

var catalog, catalogSets = mustLoad(web.StaticContent())

// Sets returns the guide sets in catalog order.
func Sets() []Set { return append([]Set(nil), catalogSets...) }

// All returns every guide: workshop guides first, then topics.
func All() []Guide { return append([]Guide(nil), catalog...) }

// Topics returns the topic guides in catalog order.
func Topics() []Guide { return ofKind(Topic) }

// WorkshopGuides returns the workshop guides in catalog order.
func WorkshopGuides() []Guide { return ofKind(Workshop) }

func ofKind(k Kind) []Guide {
	var out []Guide
	for _, g := range catalog {
		if g.Kind == k {
			out = append(out, g)
		}
	}
	return out
}

// Lookup finds a guide of either kind by slug.
func Lookup(slug string) (Guide, bool) {
	for _, g := range catalog {
		if g.Slug == slug {
			return g, true
		}
	}
	return Guide{}, false
}

// IsTopic reports whether slug is a topic guide.
func IsTopic(slug string) bool {
	g, ok := Lookup(slug)
	return ok && g.Kind == Topic
}

// IsWorkshopGuide reports whether slug is a workshop guide.
func IsWorkshopGuide(slug string) bool {
	g, ok := Lookup(slug)
	return ok && g.Kind == Workshop
}
