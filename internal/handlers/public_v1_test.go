package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// pubV1WorkshopStore is a fake publicWorkshopStore for #0729's tests.
type pubV1WorkshopStore struct {
	upcoming, past []workshops.Workshop
}

func (f pubV1WorkshopStore) ListVisible(context.Context, time.Time) ([]workshops.Workshop, []workshops.Workshop, error) {
	return f.upcoming, f.past, nil
}

func (f pubV1WorkshopStore) GetBySlug(_ context.Context, slug string) (workshops.Workshop, error) {
	for _, w := range append(append([]workshops.Workshop(nil), f.upcoming...), f.past...) {
		if w.Slug == slug {
			return w, nil
		}
	}
	return workshops.Workshop{}, workshops.ErrNotFound
}

// pubV1InterestStore resolves interest ids 1 and 2.
type pubV1InterestStore struct{}

func (pubV1InterestStore) GetByIDs(_ context.Context, ids []int64) ([]interests.Interest, error) {
	all := map[int64]interests.Interest{1: {ID: 1, Slug: "microcontrollers", Name: "Microcontrollers"}, 2: {ID: 2, Slug: "soldering", Name: "Soldering"}}
	var out []interests.Interest
	for _, id := range ids {
		if it, ok := all[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// pubV1ArchiveStore is a fake publicArchiveStore.
type pubV1ArchiveStore struct{ campaigns []mailing.Campaign }

func (f pubV1ArchiveStore) ListArchived(context.Context) ([]mailing.Campaign, error) {
	return f.campaigns, nil
}

func (f pubV1ArchiveStore) GetBySlug(_ context.Context, slug string) (mailing.Campaign, error) {
	for _, c := range f.campaigns {
		if c.Slug == slug {
			return c, nil
		}
	}
	return mailing.Campaign{}, mailing.ErrCampaignNotFound
}

const pubV1Base = "https://www.opencircuitsf.com"

func pubV1Workshop(slug string, start time.Time) workshops.Workshop {
	pub := start.Add(-30 * 24 * time.Hour)
	return workshops.Workshop{Slug: slug, Title: slug, StartsAt: &start, Status: workshops.StatusPublished, PublishedAt: &pub, EventTypeName: "Workshop"}
}

func pubV1Op(t *testing.T, reg *publicapi.Registry, name string) publicapi.Op {
	t.Helper()
	for _, op := range reg.Ops() {
		if op.Name == name {
			return op
		}
	}
	t.Fatalf("no op %s", name)
	return publicapi.Op{}
}

// pubV1Call decodes args the way the MCP adapter does and invokes the op.
func pubV1Call(t *testing.T, reg *publicapi.Registry, name, args string) (any, error) {
	t.Helper()
	op := pubV1Op(t, reg, name)
	in, err := op.Decode([]byte(args))
	if err != nil {
		return nil, err
	}
	return reg.Invoke(context.Background(), op, in)
}

func pubV1Reg(ws pubV1WorkshopStore, is publicWorkshopInterestStore, as pubV1ArchiveStore) *publicapi.Registry {
	return BuildPublicRegistry(PublicDeps{
		Workshops: NewPublicWorkshopsHandler(ws, is),
		Archive:   NewPublicArchiveHandler(as),
		About: func() publicapi.AboutText {
			return publicapi.AboutText{
				Intro:    []string{"Open Circuit SF is a group. More text.", "Second."},
				Sections: []publicapi.AboutSection{{Heading: "H", Text: "T"}},
				Links:    map[string]string{"email": "mailto:contact@opencircuitsf.com", "discord": "https://discord.gg/x", "luma": "https://lu.ma/x"},
			}
		},
		BaseURL: pubV1Base + "/", Version: "t",
	})
}

func TestPublicRegistryIsTheSixTools(t *testing.T) {
	want := [][2]string{
		{"list_upcoming_events", "/api/public/v1/events"},
		{"search_events", "/api/public/v1/events/search"},
		{"get_event", "/api/public/v1/events/{slug}"},
		{"list_newsletter_issues", "/api/public/v1/newsletter"},
		{"get_newsletter_issue", "/api/public/v1/newsletter/{slug}"},
		{"get_about", "/api/public/v1/about"},
	}
	var got [][2]string
	for _, op := range BuildPublicRegistry(PublicDeps{}).Ops() {
		got = append(got, [2]string{op.Name, op.Path})
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ops = %v, want %v", got, want)
	}
}

// TestAdminOperationsCannotBePublic: every v1 admin operation is refused by
// the public registry.
func TestAdminOperationsCannotBePublic(t *testing.T) {
	ops := BuildV1Registry(api.Config{Version: "x"}, V1Deps{}).Operations()
	if len(ops) < 20 {
		t.Fatalf("only %d admin operations; the test would be vacuous", len(ops))
	}
	for _, op := range ops {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("admin operation %s was accepted by the public registry", op.Name)
				}
			}()
			publicapi.NewRegistry("x").Add(publicapi.Op{Operation: op, Summarize: func(any) string { return "" }})
		}()
	}
}

func schemaProps(s *jsonschema.Schema, into map[string]bool) {
	if s == nil {
		return
	}
	for name, p := range s.Properties {
		into[name] = true
		schemaProps(p, into)
	}
	schemaProps(s.Items, into)
	for _, d := range s.Defs {
		schemaProps(d, into)
	}
	for _, d := range s.Definitions {
		schemaProps(d, into)
	}
}

// TestPublicOutputSchemasExposeOnlyAllowedFields pins every output field.
// A new field must be added to allowed by hand.
func TestPublicOutputSchemasExposeOnlyAllowedFields(t *testing.T) {
	allowed := []string{
		"events", "id", "slug", "title", "summary", "starts_at", "ends_at", "location_name", "address",
		"status", "registration_url", "url", "event_type", "tags", "location_note", "description",
		"issues", "published_at", "body",
		"name", "sections", "heading", "text", "website_url", "events_url", "newsletter_archive_url",
		"subscribe_url", "contact_email", "discord_url", "calendar_url",
	}
	got := map[string]bool{}
	for _, op := range BuildPublicRegistry(PublicDeps{}).Ops() {
		schemaProps(op.OutputSchema(), got)
	}
	var names []string
	for n := range got {
		names = append(names, n)
		for _, bad := range []string{"attendee", "rsvp", "subscriber", "recipient", "internal", "admin", "cost", "capacity", "audience", "created_by"} {
			if strings.Contains(n, bad) {
				t.Errorf("output field %q contains %q", n, bad)
			}
		}
	}
	slices.Sort(names)
	slices.Sort(allowed)
	if !slices.Equal(names, allowed) {
		t.Errorf("output fields = %v\nwant %v", names, allowed)
	}
}

func TestListUpcomingClampsLimit(t *testing.T) {
	now := time.Now()
	var ws pubV1WorkshopStore
	for i := range 60 {
		ws.upcoming = append(ws.upcoming, pubV1Workshop(fmt.Sprintf("e%02d", i), now.Add(time.Duration(i+1)*time.Hour)))
	}
	reg := pubV1Reg(ws, nil, pubV1ArchiveStore{})
	for args, want := range map[string]int{`{}`: 10, `{"limit":500}`: 50, `{"limit":0}`: 10, `{"limit":3}`: 3} {
		out, err := pubV1Call(t, reg, "list_upcoming_events", args)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(out.(PublicEventList).Events); n != want {
			t.Errorf("%s: %d events, want %d", args, n, want)
		}
	}
}

func TestEventFieldMapping(t *testing.T) {
	dec := time.Date(2026, 12, 3, 2, 0, 0, 0, time.UTC) // 6 PM Dec 2 Pacific
	canceled := pubV1Workshop("canceled-one", dec)
	canceled.Status = workshops.StatusCanceled
	bad := "javascript:alert(1)"
	canceled.SignupURL = &bad
	reg := pubV1Reg(pubV1WorkshopStore{upcoming: []workshops.Workshop{canceled}}, nil, pubV1ArchiveStore{})
	out, err := pubV1Call(t, reg, "get_event", `{"slug":"canceled-one"}`)
	if err != nil {
		t.Fatal(err)
	}
	ev := out.(PublicEventDetail)
	if ev.Status != "cancelled" || ev.URL != pubV1Base+"/events/canceled-one" || ev.ID != "canceled-one" {
		t.Errorf("event = %+v", ev)
	}
	b, _ := json.Marshal(ev)
	for _, want := range []string{`"starts_at":"2026-12-02T18:00:00-08:00"`, `"registration_url":null`, `"tags":[]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON lacks %s: %s", want, b)
		}
	}
	if s := pubV1Op(t, reg, "get_event").Summarize(out); strings.Contains(s, "\n") || !strings.Contains(s, "cancelled") {
		t.Errorf("summary = %q", s)
	}
}

func TestSearchEvents(t *testing.T) {
	now := time.Now()
	a := pubV1Workshop("esp32-intro", time.Date(2030, 12, 5, 18, 0, 0, 0, announceLocation))
	a.Title = "ESP32 Intro"
	a.InterestIDs = []int64{1}
	b := pubV1Workshop("solder-night", time.Date(2030, 12, 20, 18, 0, 0, 0, announceLocation))
	b.Title = "Solder night"
	b.InterestIDs = []int64{2}
	past := pubV1Workshop("old-esp32", now.Add(-48*time.Hour))
	past.Title = "Old ESP32 build"
	reg := pubV1Reg(pubV1WorkshopStore{upcoming: []workshops.Workshop{a, b}, past: []workshops.Workshop{past}}, pubV1InterestStore{}, pubV1ArchiveStore{})
	slugs := func(args string) []string {
		t.Helper()
		out, err := pubV1Call(t, reg, "search_events", args)
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		var s []string
		for _, e := range out.(PublicEventList).Events {
			s = append(s, e.Slug)
		}
		return s
	}
	for args, want := range map[string][]string{
		`{"query":"esp32 intro"}`:                 {"esp32-intro"},
		`{"query":"esp32 solder"}`:                nil,
		`{"query":"MICROCONTROLLERS"}`:            {"esp32-intro"},
		`{"query":"Soldering"}`:                   {"solder-night"},
		`{"query":"esp32","include_past":true}`:   {"esp32-intro", "old-esp32"},
		`{"from":"2030-12-10","to":"2030-12-20"}`: {"solder-night"},
		`{"to":"2030-12-05"}`:                     {"esp32-intro"},
		`{"from":"2030-12-05T18:00:00-08:00","to":"2030-12-05T18:00:00-08:00"}`: {"esp32-intro"},
		`{"query":"` + strings.Repeat("x", 500) + `"}`:                          nil,
		`{}`: {"esp32-intro", "solder-night"},
	} {
		if got := slugs(args); !slices.Equal(got, want) {
			t.Errorf("%.60s: %v, want %v", args, got, want)
		}
	}
	_, err := pubV1Call(t, reg, "search_events", `{"from":"next tuesday"}`)
	var e *api.Error
	if !errors.As(err, &e) || e.Code != api.CodeValidation || e.Fields["from"] == "" {
		t.Errorf("bad date error = %v", err)
	}
}

func TestNewsletterBodyIsPlainText(t *testing.T) {
	at := time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC)
	sent := mailing.Campaign{Slug: "nov", Subject: "November", BodyMD: "**Bold** <b>raw</b> <!-- note -->", Status: mailing.CampaignStatusSent, ArchiveStatus: mailing.ArchiveStatusPublished, ArchivedAt: &at}
	withheld := mailing.Campaign{Slug: "gone", Subject: "Gone", Status: mailing.CampaignStatusSent, ArchiveStatus: mailing.ArchiveStatusWithheld, ArchivedAt: &at}
	unsent := mailing.Campaign{Slug: "unsent", Subject: "Unsent", Status: mailing.CampaignStatusScheduled, ArchiveStatus: mailing.ArchiveStatusPublished, ArchivedAt: &at}
	reg := pubV1Reg(pubV1WorkshopStore{}, nil, pubV1ArchiveStore{campaigns: []mailing.Campaign{sent, withheld, unsent}})
	out, err := pubV1Call(t, reg, "get_newsletter_issue", `{"slug":"nov"}`)
	if err != nil {
		t.Fatal(err)
	}
	d := out.(PublicNewsletterIssueDetail)
	if !strings.Contains(d.Body, "Bold") || strings.Contains(d.Body, "<") || d.URL != pubV1Base+"/archive/nov" {
		t.Errorf("detail = %+v", d)
	}
	for _, slug := range []string{"gone", "unsent", "nope"} {
		_, err := pubV1Call(t, reg, "get_newsletter_issue", `{"slug":"`+slug+`"}`)
		var e *api.Error
		if !errors.As(err, &e) || e.Code != api.CodeNotFound || e.Message != "newsletter issue not found" {
			t.Errorf("%s: err = %v", slug, err)
		}
	}
	list, err := pubV1Call(t, reg, "list_newsletter_issues", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if is := list.(PublicNewsletterList).Issues; len(is) != 1 || is[0].Slug != "nov" {
		t.Errorf("list = %+v", is)
	}
}

func TestGetAbout(t *testing.T) {
	reg := pubV1Reg(pubV1WorkshopStore{}, nil, pubV1ArchiveStore{})
	out, err := pubV1Call(t, reg, "get_about", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	a := out.(PublicAbout)
	if a.ContactEmail != "contact@opencircuitsf.com" || a.SubscribeURL != pubV1Base+"/subscribe" ||
		a.Description != "Open Circuit SF is a group. More text.\n\nSecond." || a.Name != "Open Circuit SF" {
		t.Errorf("about = %+v", a)
	}
	if s := pubV1Op(t, reg, "get_about").Summarize(out); s != "Open Circuit SF: Open Circuit SF is a group." {
		t.Errorf("summary = %q", s)
	}
}

// TestPublicListOpsAreCached is #0730: exactly the three list operations
// use the registry's list cache.
func TestPublicListOpsAreCached(t *testing.T) {
	var got []string
	for _, op := range BuildPublicRegistry(PublicDeps{}).Ops() {
		if op.Cached {
			got = append(got, op.Name)
		}
	}
	slices.Sort(got)
	if want := []string{"list_newsletter_issues", "list_upcoming_events", "search_events"}; !slices.Equal(got, want) {
		t.Errorf("cached ops = %v, want %v", got, want)
	}
}
