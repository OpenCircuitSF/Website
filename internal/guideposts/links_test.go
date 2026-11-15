package guideposts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// fakeShortLinks stands in for ShortLinks in memory; no test reaches the
// real service. Like ShortLinks it de-duplicates links on destination.
type fakeShortLinks struct {
	mu         sync.Mutex
	enabled    bool
	err        error  // returned by CreateLink when set
	suffix     string // appended to a campaign slug, as on a race
	foreign    bool   // answer with another destination
	byDest     map[string]*shortlinks.Link
	campaignOf map[string]string // key -> campaign slug
	creates    int
	calls      int
}

func newFakeShortLinks() *fakeShortLinks {
	return &fakeShortLinks{enabled: true, byDest: map[string]*shortlinks.Link{}, campaignOf: map[string]string{}}
}

func (f *fakeShortLinks) Enabled() bool { return f.enabled }

func (f *fakeShortLinks) Me(context.Context) (*shortlinks.Me, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &shortlinks.Me{BaseURL: "https://go.example.test/"}, nil
}

func (f *fakeShortLinks) EnsureCampaign(_ context.Context, slug string, _ shortlinks.CreateCampaignRequest) (*shortlinks.Campaign, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &shortlinks.Campaign{Slug: slug + f.suffix}, nil
}

func (f *fakeShortLinks) CreateLink(_ context.Context, req shortlinks.CreateLinkRequest) (*shortlinks.Link, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.foreign {
		return &shortlinks.Link{Key: "foreign", DestinationURL: "https://elsewhere.example.test/"}, nil
	}
	if l, ok := f.byDest[req.DestinationURL]; ok {
		dup := true
		c := *l
		c.Duplicate = &dup
		return &c, nil
	}
	f.creates++
	l := &shortlinks.Link{Key: fmt.Sprintf("Fk%04d%s", f.creates, strings.ToLower(req.UTMContent)), DestinationURL: req.DestinationURL}
	f.byDest[req.DestinationURL] = l
	f.campaignOf[l.Key] = req.CampaignSlug
	return l, nil
}

func TestLinker_MintsOneLinkPerPost(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fake := newFakeShortLinks()
	l := NewLinker(s, fake)
	a := create(t, s, "baud-rate", "bluesky", "", "")
	b := create(t, s, "baud-rate", "bluesky", "", "")
	pa, _, err := l.Ensure(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	pb, _, err := l.Ensure(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *pa.ShortLinkKey == *pb.ShortLinkKey || fake.creates != 2 {
		t.Errorf("keys %q and %q, %d creates", *pa.ShortLinkKey, *pb.ShortLinkKey, fake.creates)
	}
	if *pa.ShortURL != "https://go.example.test/u/"+*pa.ShortLinkKey || !strings.HasSuffix(pa.Body, *pa.ShortURL) {
		t.Errorf("post = %+v", pa)
	}
}

func TestLinker_RetryAfterLostWriteReusesSameLink(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fake := newFakeShortLinks()
	p := create(t, s, "baud-rate", "mastodon", "", "")
	// The first mint reached ShortLinks but its local write was lost.
	first, err := fake.CreateLink(ctx, shortlinks.CreateLinkRequest{DestinationURL: p.DestinationURL})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := NewLinker(s, fake).Ensure(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *got.ShortLinkKey != first.Key || fake.creates != 1 {
		t.Errorf("key %q, want %q; %d links in the fake", *got.ShortLinkKey, first.Key, fake.creates)
	}
}

func TestLinker_AlreadyLinkedMakesNoCall(t *testing.T) {
	s := newTestStore(t)
	fake := newFakeShortLinks()
	p := linked(t, s, "bluesky")
	got, _, err := NewLinker(s, fake).Ensure(context.Background(), p.ID)
	if err != nil || *got.ShortURL != *p.ShortURL || fake.calls != 0 {
		t.Errorf("Ensure = %+v, %v; %d calls", got, err, fake.calls)
	}
}

func TestLinker_FailureKeepsDraftWithoutLink(t *testing.T) {
	s := newTestStore(t)
	fake := newFakeShortLinks()
	fake.err = shortlinks.ErrRateLimited
	p := create(t, s, "baud-rate", "bluesky", "", "")
	if _, _, err := NewLinker(s, fake).Ensure(context.Background(), p.ID); !errors.Is(err, shortlinks.ErrRateLimited) {
		t.Errorf("err = %v", err)
	}
	got, _ := s.Get(context.Background(), p.ID)
	if got.Status != "draft" || got.ShortURL != nil || got.Body != p.Body {
		t.Errorf("post = %+v", got)
	}
}

func TestLinker_UnconfiguredReturnsErrUnavailable(t *testing.T) {
	s := newTestStore(t)
	fake := newFakeShortLinks()
	fake.enabled = false
	p := create(t, s, "baud-rate", "bluesky", "", "")
	if _, _, err := NewLinker(s, fake).Ensure(context.Background(), p.ID); !errors.Is(err, shortlinks.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("%d calls", fake.calls)
	}
}

func TestLinker_RefusesForeignDestination(t *testing.T) {
	s := newTestStore(t)
	fake := newFakeShortLinks()
	fake.foreign = true
	p := create(t, s, "baud-rate", "bluesky", "", "")
	if _, _, err := NewLinker(s, fake).Ensure(context.Background(), p.ID); !errors.Is(err, ErrForeignLink) {
		t.Errorf("err = %v", err)
	}
	got, _ := s.Get(context.Background(), p.ID)
	if got.ShortURL != nil {
		t.Error("a foreign link was stored")
	}
}

func TestLinker_StoresReturnedCampaignSlug(t *testing.T) {
	s := newTestStore(t)
	fake := newFakeShortLinks()
	fake.suffix = "-2"
	p := create(t, s, "baud-rate", "bluesky", "", "")
	got, _, err := NewLinker(s, fake).Ensure(context.Background(), p.ID)
	if err != nil || got.CampaignSlug != CampaignSlug+"-2" || fake.campaignOf[*got.ShortLinkKey] != CampaignSlug+"-2" {
		t.Errorf("post = %+v, %v", got, err)
	}
}

// TestLinker_MintsIntoEachDraftsCampaign covers the user's 2026-11-14
// request: a repost can name a new campaign, and its link is minted there.
func TestLinker_MintsIntoEachDraftsCampaign(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fake := newFakeShortLinks()
	l := NewLinker(s, fake)
	a := create(t, s, "baud-rate", "bluesky", "", "")
	b := create(t, s, "baud-rate", "bluesky", "", "guides-repost-2027")
	pa, _, err := l.Ensure(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	pb, _, err := l.Ensure(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fake.campaignOf[*pa.ShortLinkKey] != CampaignSlug || pa.CampaignSlug != CampaignSlug {
		t.Errorf("default draft minted into %q / stored %q", fake.campaignOf[*pa.ShortLinkKey], pa.CampaignSlug)
	}
	if fake.campaignOf[*pb.ShortLinkKey] != "guides-repost-2027" || pb.CampaignSlug != "guides-repost-2027" {
		t.Errorf("repost minted into %q / stored %q", fake.campaignOf[*pb.ShortLinkKey], pb.CampaignSlug)
	}
}

func TestLinker_EnsureReportsMintedOnlyOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	fake := newFakeShortLinks()
	l := NewLinker(s, fake)
	p := create(t, s, "baud-rate", "bluesky", "", "")
	if _, minted, err := l.Ensure(ctx, p.ID); err != nil || !minted {
		t.Fatalf("first: minted=%v err=%v", minted, err)
	}
	if _, minted, err := l.Ensure(ctx, p.ID); err != nil || minted {
		t.Fatalf("second: minted=%v err=%v", minted, err)
	}
	if fake.creates != 1 {
		t.Errorf("creates = %d, want 1", fake.creates)
	}
}
