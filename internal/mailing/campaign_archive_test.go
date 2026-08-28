// Tests for campaign_archive.go's GetBySlug/ListArchived/SetArchiveStatus,
// plus campaigns.go's slug-minting (Create) and slug-immutability (Update)
// behavior added by #0123 (PRD §6.8).
package mailing

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ── Slug minting (Create) ────────────────────────────────────────────────────

func TestCampaignStore_Create_MintsSlugFromSubject(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	name := uniqueCampaignName(t)

	c, err := store.Create(context.Background(), CampaignInput{
		Name:         name,
		Subject:      "Hello, World! Solder Night #3",
		BodyMD:       "# Hello",
		AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if c.Slug != "hello-world-solder-night-3" {
		t.Errorf("Slug = %q, want %q", c.Slug, "hello-world-solder-night-3")
	}
	if c.ArchiveStatus != ArchiveStatusPending {
		t.Errorf("ArchiveStatus = %q, want %q", c.ArchiveStatus, ArchiveStatusPending)
	}
	if c.ArchivedAt != nil {
		t.Errorf("ArchivedAt = %v, want nil", c.ArchivedAt)
	}
}

// TestCampaignStore_Create_UniquifiesSlugOnCollision is this issue's core
// acceptance criterion: "uniquified on collision". Two campaigns with the
// identical subject must get two DIFFERENT slugs, the second suffixed.
func TestCampaignStore_Create_UniquifiesSlugOnCollision(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	subject := uniqueCampaignName(t) + " collision subject"

	first, err := store.Create(context.Background(), CampaignInput{
		Name: "first", Subject: subject, BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	cleanupCampaign(t, pool, first.ID)

	second, err := store.Create(context.Background(), CampaignInput{
		Name: "second", Subject: subject, BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	cleanupCampaign(t, pool, second.ID)

	if first.Slug == second.Slug {
		t.Fatalf("both campaigns got the same slug %q", first.Slug)
	}
	if second.Slug != first.Slug+"-2" {
		t.Errorf("second.Slug = %q, want %q", second.Slug, first.Slug+"-2")
	}
}

func TestCampaignStore_Create_EmptySubjectFallsBackToPlaceholderSlug(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "!!!", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if c.Slug != "campaign" {
		t.Errorf("Slug = %q, want %q", c.Slug, "campaign")
	}
}

// ── Slug immutability (Update) ───────────────────────────────────────────────

func TestCampaignStore_Update_SlugEditableWhileDraft(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	newSlug := uniqueCampaignName(t) + "-renamed"
	updated, err := store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: c.Subject, BodyMD: c.BodyMD, AudienceMode: c.AudienceMode,
		Slug: newSlug,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Slug != newSlug {
		t.Errorf("Slug = %q, want %q", updated.Slug, newSlug)
	}
}

// TestCampaignStore_Update_SlugImmutableOnceScheduled is this issue's other
// core acceptance criterion: "immutable once the campaign leaves draft".
func TestCampaignStore_Update_SlugImmutableOnceScheduled(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	if _, err := store.Send(context.Background(), c.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, err = store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: c.Subject, BodyMD: c.BodyMD, AudienceMode: c.AudienceMode,
		Slug: c.Slug + "-different",
	})
	if !errors.Is(err, ErrCampaignSlugNotEditable) {
		t.Fatalf("Update slug on scheduled campaign: err = %v, want ErrCampaignSlugNotEditable", err)
	}

	// Passing back the UNCHANGED slug must still be accepted while
	// scheduled -- CampaignUpdate.Slug's own doc comment.
	updated, err := store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: c.Subject, BodyMD: c.BodyMD, AudienceMode: c.AudienceMode,
		Slug: c.Slug,
	})
	if err != nil {
		t.Fatalf("Update with unchanged slug on scheduled campaign: %v", err)
	}
	if updated.Slug != c.Slug {
		t.Errorf("Slug = %q, want unchanged %q", updated.Slug, c.Slug)
	}
}

func TestCampaignStore_Update_SlugCollisionReturnsTypedError(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	a, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject A " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	cleanupCampaign(t, pool, a.ID)
	b, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject B " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	cleanupCampaign(t, pool, b.ID)

	_, err = store.Update(context.Background(), b.ID, CampaignUpdate{
		Name: b.Name, Subject: b.Subject, BodyMD: b.BodyMD, AudienceMode: b.AudienceMode,
		Slug: a.Slug,
	})
	if !errors.Is(err, ErrCampaignSlugTaken) {
		t.Fatalf("Update to a taken slug: err = %v, want ErrCampaignSlugTaken", err)
	}
}

// TestCampaignStore_Update_ZeroValueSlugKeepsCurrent is the store-level half
// of the review-of-#0123 fix (2026-08-27): AdminCampaignsHandler.Patch used
// to build a mailing.CampaignUpdate with no Slug field at all, so every
// PATCH sent the zero value and Update wrote "" straight into the column --
// destroying a draft's archive URL on an ordinary content edit, and 500ing
// on a scheduled campaign's edit (ErrCampaignSlugNotEditable, unmapped by
// the handler) or a second draft's edit (the UNIQUE(slug) constraint, two
// rows both wanting an empty slug). Update now treats an empty in.Slug as
// "keep current" rather than as a value to write -- this proves that directly,
// bypassing the handler entirely, so the guard holds even if some future
// caller repeats the handler's old omission.
func TestCampaignStore_Update_ZeroValueSlugKeepsCurrent(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	if c.Slug == "" {
		t.Fatalf("seeded campaign has empty slug, precondition failed")
	}

	updated, err := store.Update(context.Background(), c.ID, CampaignUpdate{
		Name: c.Name, Subject: "new subject", BodyMD: c.BodyMD, AudienceMode: c.AudienceMode,
		// Slug deliberately omitted -- the zero value, exactly what the
		// pre-fix handler sent on every PATCH.
	})
	if err != nil {
		t.Fatalf("Update with zero-value Slug: %v", err)
	}
	if updated.Slug != c.Slug {
		t.Errorf("Slug = %q after zero-value-Slug Update, want unchanged %q", updated.Slug, c.Slug)
	}
	if updated.Subject != "new subject" {
		t.Errorf("Subject = %q, want %q (other fields must still apply)", updated.Subject, "new subject")
	}

	// Read back from the database, not just the returned struct.
	reloaded, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if reloaded.Slug != c.Slug {
		t.Errorf("stored Slug = %q, want unchanged %q", reloaded.Slug, c.Slug)
	}
}

// ── GetBySlug ─────────────────────────────────────────────────────────────────

func TestCampaignStore_GetBySlug_Found(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	got, err := store.GetBySlug(context.Background(), c.Slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("ID = %d, want %d", got.ID, c.ID)
	}
}

func TestCampaignStore_GetBySlug_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	_, err := store.GetBySlug(context.Background(), "zz-no-such-slug-"+uniqueCampaignName(t))
	if !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("GetBySlug: err = %v, want ErrCampaignNotFound", err)
	}
}

// ── ListArchived ──────────────────────────────────────────────────────────────

func TestCampaignStore_ListArchived_OnlyPublished(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	draft, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Draft " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	cleanupCampaign(t, pool, draft.ID)

	sent, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Sent " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create sent: %v", err)
	}
	cleanupCampaign(t, pool, sent.ID)
	// Simulate the worker's CompleteIfDone stamp directly -- this package's
	// own API has no other path to 'published' short of a real send.
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'sent', archive_status = 'published', archived_at = now() WHERE id = $1`,
		sent.ID); err != nil {
		t.Fatalf("stamp sent campaign published: %v", err)
	}

	withheld, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Withheld " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create withheld: %v", err)
	}
	cleanupCampaign(t, pool, withheld.ID)
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'sent', archive_status = 'withheld', archived_at = now() WHERE id = $1`,
		withheld.ID); err != nil {
		t.Fatalf("stamp withheld campaign: %v", err)
	}

	list, err := store.ListArchived(context.Background())
	if err != nil {
		t.Fatalf("ListArchived: %v", err)
	}
	var sawSent, sawDraft, sawWithheld bool
	for _, c := range list {
		switch c.ID {
		case sent.ID:
			sawSent = true
		case draft.ID:
			sawDraft = true
		case withheld.ID:
			sawWithheld = true
		}
	}
	if !sawSent {
		t.Error("ListArchived did not include the published campaign")
	}
	if sawDraft {
		t.Error("ListArchived included a draft (pending) campaign")
	}
	if sawWithheld {
		t.Error("ListArchived included a withheld campaign")
	}
}

// ── SetArchiveStatus ──────────────────────────────────────────────────────────

func TestCampaignStore_SetArchiveStatus_PublishedToWithheldAndBack(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'sent', archive_status = 'published', archived_at = now() WHERE id = $1`,
		c.ID); err != nil {
		t.Fatalf("stamp published: %v", err)
	}

	withheld, err := store.SetArchiveStatus(context.Background(), c.ID, ArchiveStatusWithheld)
	if err != nil {
		t.Fatalf("SetArchiveStatus withheld: %v", err)
	}
	if withheld.ArchiveStatus != ArchiveStatusWithheld {
		t.Errorf("ArchiveStatus = %q, want %q", withheld.ArchiveStatus, ArchiveStatusWithheld)
	}
	if withheld.ArchivedAt == nil {
		t.Error("ArchivedAt was cleared by withholding; it should stay set (own doc comment)")
	}

	republished, err := store.SetArchiveStatus(context.Background(), c.ID, ArchiveStatusPublished)
	if err != nil {
		t.Fatalf("SetArchiveStatus republish: %v", err)
	}
	if republished.ArchiveStatus != ArchiveStatusPublished {
		t.Errorf("ArchiveStatus = %q, want %q", republished.ArchiveStatus, ArchiveStatusPublished)
	}
}

// TestCampaignStore_SetArchiveStatus_RefusesWhilePending is this file's own
// acceptance criterion: a campaign that has never been sent has no archive
// page to toggle.
func TestCampaignStore_SetArchiveStatus_RefusesWhilePending(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	_, err = store.SetArchiveStatus(context.Background(), c.ID, ArchiveStatusWithheld)
	if !errors.Is(err, ErrArchiveStatusNotEditable) {
		t.Fatalf("SetArchiveStatus on a pending campaign: err = %v, want ErrArchiveStatusNotEditable", err)
	}
}

func TestCampaignStore_SetArchiveStatus_UnknownStatus(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "Subject " + uniqueCampaignName(t), BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)

	_, err = store.SetArchiveStatus(context.Background(), c.ID, "pending")
	if !errors.Is(err, ErrUnknownArchiveStatus) {
		t.Fatalf("SetArchiveStatus(pending): err = %v, want ErrUnknownArchiveStatus", err)
	}
}

func TestCampaignStore_SetArchiveStatus_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewCampaignStore(pool)

	_, err := store.SetArchiveStatus(context.Background(), -1, ArchiveStatusWithheld)
	if !errors.Is(err, ErrCampaignNotFound) {
		t.Fatalf("SetArchiveStatus(-1): err = %v, want ErrCampaignNotFound", err)
	}
}
