package outreach

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// seedEmailCampaign inserts a throwaway email_campaigns row and deletes it on
// cleanup.
func seedEmailCampaign(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	slug := fmt.Sprintf("zz-nl-test-%d", testdb.Unique())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO email_campaigns (name, subject, body_md, slug) VALUES ('t', 't', '', $1) RETURNING id`, slug).Scan(&id); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, id) })
	return id
}

func TestInsertNewsletterLink_Idempotent(t *testing.T) {
	s := NewStore(testPool(t))
	w := seedWorkshop(t, s.pool)
	c := seedEmailCampaign(t, s.pool)
	ctx := context.Background()
	a, created, err := s.InsertNewsletterLink(ctx, c, w.ID, "camp", "k1", "https://go.example/u/k1")
	if err != nil || !created {
		t.Fatalf("first insert: %v created=%v", err, created)
	}
	b, created, err := s.InsertNewsletterLink(ctx, c, w.ID, "camp", "k2", "https://go.example/u/k2")
	if err != nil || created {
		t.Fatalf("second insert: %v created=%v", err, created)
	}
	if b.ID != a.ID || b.Key != "k1" || b.ShortURL != "https://go.example/u/k1" || b.CampaignSlug != "camp" {
		t.Errorf("second insert returned %+v, want %+v", b, a)
	}
	got, err := s.GetNewsletterLink(ctx, c, w.ID)
	if err != nil || got.ID != a.ID {
		t.Errorf("get = %+v err=%v", got, err)
	}
}

func TestGetNewsletterLink_NotFound(t *testing.T) {
	s := NewStore(testPool(t))
	w := seedWorkshop(t, s.pool)
	c := seedEmailCampaign(t, s.pool)
	if _, err := s.GetNewsletterLink(context.Background(), c, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNewsletterLink_CascadesOnCampaignDelete(t *testing.T) {
	s := NewStore(testPool(t))
	w := seedWorkshop(t, s.pool)
	c := seedEmailCampaign(t, s.pool)
	ctx := context.Background()
	if _, _, err := s.InsertNewsletterLink(ctx, c, w.ID, "camp", "k", "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM email_campaigns WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetNewsletterLink(ctx, c, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}
