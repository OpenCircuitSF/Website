package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// TestAnnounceWhen_PacificTimeZone_PinnedInstants is #0144's own acceptance
// criterion: the announce body renders start/end in America/Los_Angeles,
// zone labeled, and the rendered string is pinned for a known instant so a
// future formatting change shows up in the diff. Two instants are used
// deliberately on opposite sides of America/Los_Angeles' 2026 DST
// boundaries (spring forward 2026-03-08, fall back 2026-11-01) — a test
// that only ever ran in one offset would prove only half the behavior.
// Expected strings were computed independently with Python's zoneinfo
// (authoritative IANA tzdata), not derived from the code under test.
func TestAnnounceWhen_PacificTimeZone_PinnedInstants(t *testing.T) {
	mustParse := func(s string) time.Time {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return tm
	}

	tests := []struct {
		name   string
		starts string
		ends   string
		want   string
	}{
		{
			// Standard time (PST, UTC-8). 02:00Z Jan 15 is still Jan 14
			// evening in Pacific — also exercises the "same Pacific day,
			// different UTC day" edge the doc comment calls out.
			name:   "winter standard time (PST)",
			starts: "2026-01-15T02:00:00Z",
			ends:   "2026-01-15T04:00:00Z",
			want:   "Wednesday, January 14, 2026 at 6:00 PM PST to 8:00 PM PST",
		},
		{
			// Daylight time (PDT, UTC-7), after the 2026-03-08 spring-forward
			// and before the 2026-11-01 fall-back.
			name:   "summer daylight time (PDT)",
			starts: "2026-07-15T01:00:00Z",
			ends:   "2026-07-15T03:00:00Z",
			want:   "Tuesday, July 14, 2026 at 6:00 PM PDT to 8:00 PM PDT",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starts := mustParse(tc.starts)
			ends := mustParse(tc.ends)
			wk := workshops.Workshop{StartsAt: &starts, EndsAt: &ends}
			got := announceWhen(wk)
			if got != tc.want {
				t.Errorf("announceWhen() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAnnounceWhen_StartOnly covers the no-EndsAt path, still in Pacific
// time — a single instant is enough here since the DST behavior is fully
// covered by the range test above.
func TestAnnounceWhen_StartOnly(t *testing.T) {
	starts, err := time.Parse(time.RFC3339, "2026-01-15T02:00:00Z")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wk := workshops.Workshop{StartsAt: &starts}
	got := announceWhen(wk)
	want := "Wednesday, January 14, 2026 at 6:00 PM PST"
	if got != want {
		t.Errorf("announceWhen() = %q, want %q", got, want)
	}
}

// TestAnnounceWhen_SameLocalDate_ComparesPacificNotUTC is #0156 item 1: a
// regression test for sameLocalDate's day-boundary comparison. #0144's
// reviewer found that reverting sameLocalDate from Pacific calendar days
// back to UTC calendar days left the entire internal/handlers suite green —
// nothing committed defended the Pacific-vs-UTC choice. These two cases,
// carried over verbatim (instants and expected strings) from #0144's
// `## Review notes` mutation spot-check, each fail under exactly one of the
// two comparison rules, so together they pin the Pacific behavior and would
// catch the same reversion.
func TestAnnounceWhen_SameLocalDate_ComparesPacificNotUTC(t *testing.T) {
	tests := []struct {
		name   string
		starts string
		ends   string
		want   string
	}{
		{
			// Spans a UTC calendar-day boundary (Jan 14 -> Jan 15 UTC) but
			// not a Pacific one (both instants are Jan 14 in Pacific,
			// 2:00 PM and 5:00 PM PST) — must render the short
			// same-day form. Under the old UTC comparison this would have
			// been treated as two different days and rendered in full.
			name:   "spans UTC day, not Pacific day -> short form",
			starts: "2026-01-14T22:00:00Z",
			ends:   "2026-01-15T01:00:00Z",
			want:   "Wednesday, January 14, 2026 at 2:00 PM PST to 5:00 PM PST",
		},
		{
			// Spans a Pacific calendar-day boundary (Jan 14 11:00 PM ->
			// Jan 15 1:00 AM PST) but not a UTC one (both instants are
			// Jan 15 in UTC) — must render the long two-timestamp form.
			// Under the old UTC comparison this would have collapsed to
			// the bare same-day "11:00 PM to 1:00 AM" form.
			name:   "spans Pacific day, not UTC day -> long form",
			starts: "2026-01-15T07:00:00Z",
			ends:   "2026-01-15T09:00:00Z",
			want:   "Wednesday, January 14, 2026 at 11:00 PM PST to Thursday, January 15, 2026 at 1:00 AM PST",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starts := mustParseAnnounceTime(t, tc.starts)
			ends := mustParseAnnounceTime(t, tc.ends)
			wk := workshops.Workshop{StartsAt: &starts, EndsAt: &ends}
			got := announceWhen(wk)
			if got != tc.want {
				t.Errorf("announceWhen() = %q, want %q", got, tc.want)
			}
		})
	}
}

func mustParseAnnounceTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return tm
}

// TestAnnounceBodyMD_AlwaysLinksToWorkshopPage is #0145's own acceptance
// criterion: the body always links to baseURL+"/workshops/{slug}", and when
// signup_url is set it remains the primary action rather than being
// replaced by the site link.
func TestAnnounceBodyMD_AlwaysLinksToWorkshopPage(t *testing.T) {
	const baseURL = "https://www.opencircuitsf.com"

	t.Run("with signup_url — external link stays primary, site link supplements", func(t *testing.T) {
		signup := "https://example.com/rsvp"
		summary := "Hands-on soldering for beginners. All tools provided."
		starts := mustParseAnnounceTime(t, "2026-09-12T18:00:00Z")
		ends := mustParseAnnounceTime(t, "2026-09-12T20:00:00Z")
		locationName := "The Shop"
		locationAddress := "123 Circuit Ave, San Francisco, CA"
		wk := workshops.Workshop{
			Title:           "Intro to Soldering",
			Slug:            "intro-to-soldering",
			Summary:         &summary,
			StartsAt:        &starts,
			EndsAt:          &ends,
			LocationName:    &locationName,
			LocationAddress: &locationAddress,
			SignupURL:       &signup,
		}
		body := announceBodyMD(wk, baseURL)
		t.Logf("RENDERED BODY (with signup_url):\n%s", body)

		wantSiteURL := "https://www.opencircuitsf.com/workshops/intro-to-soldering"
		if !strings.Contains(body, "[Sign up](https://example.com/rsvp)") {
			t.Errorf("body missing external Sign up link:\n%s", body)
		}
		if !strings.Contains(body, "[View this workshop]("+wantSiteURL+")") {
			t.Errorf("body missing site link %q:\n%s", wantSiteURL, body)
		}
		signupIdx := strings.Index(body, signup)
		siteIdx := strings.Index(body, wantSiteURL)
		if signupIdx == -1 || siteIdx == -1 || signupIdx > siteIdx {
			t.Errorf("expected external signup_url link before the site link; body:\n%s", body)
		}
	})

	t.Run("without signup_url — site link is the only call to action", func(t *testing.T) {
		summary := "Bring your broken toys. We'll bend them into instruments."
		starts := mustParseAnnounceTime(t, "2026-01-15T02:00:00Z")
		locationName := "Open Circuit SF"
		locationAddress := "456 Maker Way, San Francisco, CA"
		wk := workshops.Workshop{
			Title:           "In-House Circuit Bending Night",
			Slug:            "in-house-circuit-bending-night",
			Summary:         &summary,
			StartsAt:        &starts,
			LocationName:    &locationName,
			LocationAddress: &locationAddress,
		}
		body := announceBodyMD(wk, baseURL)
		t.Logf("RENDERED BODY (no signup_url):\n%s", body)

		wantSiteURL := "https://www.opencircuitsf.com/workshops/in-house-circuit-bending-night"
		if !strings.Contains(body, "[View this workshop]("+wantSiteURL+")") {
			t.Errorf("body missing site link %q (this is the whole point of #0145):\n%s", wantSiteURL, body)
		}
		if strings.Contains(body, "[Sign up](") {
			t.Errorf("body should not contain a bare Sign up link when signup_url is unset:\n%s", body)
		}
	})

	t.Run("baseURL with a trailing slash never produces a double slash", func(t *testing.T) {
		wk := workshops.Workshop{Title: "Test", Slug: "test-workshop"}
		body := announceBodyMD(wk, "https://www.opencircuitsf.com/")
		want := "https://www.opencircuitsf.com/workshops/test-workshop"
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q (no double slash)", body, want)
		}
		if strings.Contains(body, "opencircuitsf.com//workshops") {
			t.Errorf("body contains a double slash:\n%s", body)
		}
	})
}
