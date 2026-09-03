// Store-level tests for #0405's opt-in newsletter-month slug template on
// CampaignStore.Create (PRD §6.8). A new file, deliberately, so
// campaign_archive_test.go's own slug tests
// (TestCampaignStore_Create_MintsSlugFromSubject,
// TestCampaignStore_Create_UniquifiesSlugOnCollision,
// TestCampaignStore_Create_EmptySubjectFallsBackToPlaceholderSlug) are
// provably unedited — issues/0405.md criterion 4.
//
// Every database-backed test here uses month 2031-03 (slug "03-2031"): far
// from any real campaign and from #0404's production backfill on id 1
// (issues/0405.md §6 — that production literal must never appear under
// internal/ or web/src/, and these tests must never be able to contend
// with it).
package mailing

import (
	"context"
	"errors"
	"testing"
)

func TestCampaignStore_Create_NewsletterMonthMintsTemplateSlug(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	nm, err := ParseNewsletterMonth("2031-03")
	if err != nil {
		t.Fatalf("ParseNewsletterMonth: %v", err)
	}

	c, err := store.Create(context.Background(), CampaignInput{
		Name:            uniqueCampaignName(t),
		Subject:         "Ordinary March Update",
		BodyMD:          "b",
		AudienceMode:    AudienceAll,
		NewsletterMonth: &nm,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if c.Slug != "03-2031" {
		t.Errorf("Slug = %q, want %q", c.Slug, "03-2031")
	}
}

// TestCampaignStore_Create_SecondNewsletterInSameMonthGetsSuffix mirrors
// TestCampaignStore_Create_UniquifiesSlugOnCollision's shape (relative
// assertion, not a hardcoded suffix) — issues/0405.md §3's answer to "a
// second newsletter in one month": the existing collision algorithm must
// not change, and a suffixed second slug (03-2031-2 here) is the correct
// outcome.
func TestCampaignStore_Create_SecondNewsletterInSameMonthGetsSuffix(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	nm, err := ParseNewsletterMonth("2031-03")
	if err != nil {
		t.Fatalf("ParseNewsletterMonth: %v", err)
	}

	first, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "March Newsletter (first)", BodyMD: "b",
		AudienceMode: AudienceAll, NewsletterMonth: &nm,
	})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	cleanupCampaign(t, pool, first.ID)

	second, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "March Newsletter (second)", BodyMD: "b",
		AudienceMode: AudienceAll, NewsletterMonth: &nm,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	cleanupCampaign(t, pool, second.ID)

	if first.Slug != "03-2031" {
		t.Fatalf("first.Slug = %q, want %q", first.Slug, "03-2031")
	}
	if second.Slug != first.Slug+"-2" {
		t.Errorf("second.Slug = %q, want %q", second.Slug, first.Slug+"-2")
	}
}

// TestCampaignStore_Create_NewsletterMonthIgnoresPunctuationOnlySubject
// proves the template replaces the subject-derived path ENTIRELY, including
// its "campaign" fallback for a subject with nothing sluggable in it —
// contrast with TestCampaignStore_Create_EmptySubjectFallsBackToPlaceholderSlug,
// which covers the same subject with no NewsletterMonth set.
func TestCampaignStore_Create_NewsletterMonthIgnoresPunctuationOnlySubject(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	nm, err := ParseNewsletterMonth("2031-03")
	if err != nil {
		t.Fatalf("ParseNewsletterMonth: %v", err)
	}

	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "!!!", BodyMD: "b",
		AudienceMode: AudienceAll, NewsletterMonth: &nm,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if c.Slug != "03-2031" {
		t.Errorf("Slug = %q, want %q (not the subject-fallback %q)", c.Slug, "03-2031", "campaign")
	}
}

// TestCampaignStore_Create_WithoutNewsletterMonthStillMintsFromSubject is
// the opt-in default: a nil NewsletterMonth (the zero value of the field,
// and what every pre-#0405 caller still passes) behaves exactly as before.
func TestCampaignStore_Create_WithoutNewsletterMonthStillMintsFromSubject(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Plain Subject Line", BodyMD: "b",
		AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if c.Slug != "plain-subject-line" {
		t.Errorf("Slug = %q, want %q", c.Slug, "plain-subject-line")
	}
}

// TestCampaignStore_Create_RejectsInvalidNewsletterMonth asserts both the
// typed error AND that no row was inserted (issues/0405.md §7 test 8) — a
// zero-value NewsletterMonth is what a caller gets by constructing the
// struct directly rather than through ParseNewsletterMonth.
func TestCampaignStore_Create_RejectsInvalidNewsletterMonth(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)
	var zero NewsletterMonth

	_, err := store.Create(context.Background(), CampaignInput{
		Name: name, Subject: "Whatever", BodyMD: "b",
		AudienceMode: AudienceAll, NewsletterMonth: &zero,
	})
	if !errors.Is(err, ErrInvalidNewsletterMonth) {
		t.Fatalf("err = %v, want ErrInvalidNewsletterMonth", err)
	}

	var count int
	if scanErr := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM email_campaigns WHERE name = $1`, name).Scan(&count); scanErr != nil {
		t.Fatalf("count check: %v", scanErr)
	}
	if count != 0 {
		t.Errorf("email_campaigns row count = %d, want 0 (rejected create must not write a row)", count)
	}
}
