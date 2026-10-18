package outreach

import (
	"context"
	"errors"
	"testing"
)

func TestInsertPostLinkIsIdempotentPerSlot(t *testing.T) {
	s := NewStore(testPool(t))
	o, _ := seedPost(t, s, "x")
	ctx := context.Background()

	if _, err := s.GetPostLink(ctx, o.ID, "mastodon", "reminder"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty slot err = %v, want ErrNotFound", err)
	}
	a, created, err := s.InsertPostLink(ctx, o.ID, "mastodon", "reminder", "k1", "https://go.example/k1")
	if err != nil || !created {
		t.Fatalf("first insert: %v created=%v", err, created)
	}
	b, created, err := s.InsertPostLink(ctx, o.ID, "mastodon", "reminder", "k2", "https://go.example/k2")
	if err != nil || created {
		t.Fatalf("second insert: %v created=%v", err, created)
	}
	if b.ID != a.ID || b.Key != "k1" || b.ShortURL != "https://go.example/k1" {
		t.Errorf("second insert returned %+v, want the first row %+v", b, a)
	}
	got, err := s.GetPostLink(ctx, o.ID, "mastodon", "reminder")
	if err != nil || got.ID != a.ID {
		t.Errorf("get = %+v err=%v", got, err)
	}
	if _, _, err := s.InsertPostLink(ctx, o.ID, "mastodon", "announce", "k3", "https://go.example/k3"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListPostLinks(ctx, o.ID)
	if err != nil || len(list) != 2 || list[0].Wave != "reminder" || list[1].Wave != "announce" {
		t.Errorf("list = %+v err=%v", list, err)
	}
}
