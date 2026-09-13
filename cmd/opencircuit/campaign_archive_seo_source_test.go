package main

import (
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// TestToSEOArchiveEntry_PublishedRequiresSentAndArchivePublished pins
// #0519's tightened rule for toSEOArchiveEntry's Published field:
// PublicArchiveHandler.GetBySlug's full two-part rule (Status ==
// CampaignStatusSent AND ArchiveStatus == ArchiveStatusPublished), not
// archive_status alone. Needs no database -- toSEOArchiveEntry is a pure
// narrowing function over an in-memory mailing.Campaign value.
func TestToSEOArchiveEntry_PublishedRequiresSentAndArchivePublished(t *testing.T) {
	base := mailing.Campaign{
		Slug:    "sept-newsletter",
		Subject: "September Newsletter",
		BodyMD:  "# Hello\n\nBody text.",
	}

	t.Run("sent and published is Published=true", func(t *testing.T) {
		c := base
		c.Status = mailing.CampaignStatusSent
		c.ArchiveStatus = mailing.ArchiveStatusPublished
		got := toSEOArchiveEntry(c)
		if !got.Published {
			t.Errorf("Published = false, want true for sent+published")
		}
		if got.BodyMD != c.BodyMD {
			t.Errorf("BodyMD = %q, want %q -- toSEOArchiveEntry must carry the campaign body through", got.BodyMD, c.BodyMD)
		}
	})

	t.Run("sent and withheld is Published=false", func(t *testing.T) {
		c := base
		c.Status = mailing.CampaignStatusSent
		c.ArchiveStatus = mailing.ArchiveStatusWithheld
		if got := toSEOArchiveEntry(c); got.Published {
			t.Errorf("Published = true, want false for sent+withheld")
		}
	})

	t.Run("not sent but archive_status published is Published=false", func(t *testing.T) {
		// This is the exact case #0519 closes: before this issue,
		// Published was ArchiveStatus == Published alone, so a campaign
		// whose archive_status was (hypothetically, or via a future bug)
		// 'published' while Status was still 'draft'/'scheduled'/'sending'
		// would have counted as a real archive page. SetArchiveStatus only
		// toggles rows already past 'pending' today, so this changes no
		// CURRENT row -- it closes the gap for any future one.
		c := base
		c.Status = mailing.CampaignStatusDraft
		c.ArchiveStatus = mailing.ArchiveStatusPublished
		if got := toSEOArchiveEntry(c); got.Published {
			t.Errorf("Published = true, want false when Status is not sent, even with ArchiveStatus published")
		}
	})

	t.Run("ArchivedAt is carried as a full RFC 3339 UTC timestamp", func(t *testing.T) {
		// #0519's fix for the UTC-vs-Pacific date defect: ArchiveEntry needs
		// the full timestamp (not just UpdatedAt's bare date) for
		// fallback.go's archiveDateLabel to convert into America/Los_Angeles
		// correctly.
		c := base
		c.Status = mailing.CampaignStatusSent
		c.ArchiveStatus = mailing.ArchiveStatusPublished
		archived := time.Date(2026, 9, 13, 1, 18, 11, 0, time.UTC)
		c.ArchivedAt = &archived
		got := toSEOArchiveEntry(c)
		wantArchivedAt := "2026-09-13T01:18:11Z"
		if got.ArchivedAt != wantArchivedAt {
			t.Errorf("ArchivedAt = %q, want %q", got.ArchivedAt, wantArchivedAt)
		}
		wantUpdatedAt := "2026-09-13"
		if got.UpdatedAt != wantUpdatedAt {
			t.Errorf("UpdatedAt = %q, want %q (unchanged, for the sitemap's <lastmod>)", got.UpdatedAt, wantUpdatedAt)
		}
	})

	t.Run("nil ArchivedAt carries through as empty string", func(t *testing.T) {
		c := base
		c.Status = mailing.CampaignStatusSent
		c.ArchiveStatus = mailing.ArchiveStatusPublished
		c.ArchivedAt = nil
		got := toSEOArchiveEntry(c)
		if got.ArchivedAt != "" {
			t.Errorf("ArchivedAt = %q, want \"\" for a nil ArchivedAt", got.ArchivedAt)
		}
		if got.UpdatedAt != "" {
			t.Errorf("UpdatedAt = %q, want \"\" for a nil ArchivedAt", got.UpdatedAt)
		}
	})
}
