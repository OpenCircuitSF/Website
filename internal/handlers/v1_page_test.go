package handlers

import (
	"errors"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

func TestPaginate(t *testing.T) {
	var items []int
	for i := 1; i <= 120; i++ {
		items = append(items, i)
	}
	key := func(n int) int { return n }
	after := func(n, k int) bool { return n > k }

	first, err := paginate(items, api.Page{}, key, after)
	if err != nil || len(first.Items) != api.DefaultLimit || first.NextCursor == nil {
		t.Fatalf("default page: %d items, next %v, err %v", len(first.Items), first.NextCursor, err)
	}

	var walked []int
	cursor := ""
	pages := 0
	for {
		page, err := paginate(items, api.Page{Cursor: cursor}, key, after)
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, page.Items...)
		pages++
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if pages != 3 || len(walked) != 120 || walked[0] != 1 || walked[119] != 120 {
		t.Fatalf("walk: %d pages, %d items", pages, len(walked))
	}

	two := 2
	exact, err := paginate([]int{1, 2}, api.Page{Limit: &two}, key, after)
	if err != nil || len(exact.Items) != 2 || exact.NextCursor != nil {
		t.Fatalf("exact page: %v next %v err %v", exact.Items, exact.NextCursor, err)
	}

	empty, err := paginate([]int(nil), api.Page{}, key, after)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("empty: %#v %v", empty.Items, err)
	}

	var ae *api.Error
	if _, err := paginate(items, api.Page{Cursor: "garbage!"}, key, after); !errors.As(err, &ae) || ae.Fields["cursor"] == "" {
		t.Fatalf("garbage cursor err = %v", err)
	}
	zero := 0
	if _, err := paginate(items, api.Page{Limit: &zero}, key, after); !errors.As(err, &ae) || ae.Fields["limit"] == "" {
		t.Fatalf("limit 0 err = %v", err)
	}
}
