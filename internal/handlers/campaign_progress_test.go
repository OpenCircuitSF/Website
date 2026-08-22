package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// fakeCampaignProgressBroker records every event handed to PublishAll, so a
// test can inspect exactly what campaignProgressPublisher broadcast without
// standing up a real *events.Broker and subscriber channel.
type fakeCampaignProgressBroker struct {
	events []events.Event
}

func (f *fakeCampaignProgressBroker) PublishAll(event events.Event) {
	f.events = append(f.events, event)
}

func discardTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestNewCampaignProgressPublisher_NilBroker_ReturnsGenuinelyNilInterface
// mirrors NewCampaignPreflightChecker's own nil-typed-nil-trap proof
// (admin_campaigns_test.go): a nil *events.Broker must produce a literally
// nil mailing.ProgressPublisher, not a non-nil interface wrapping a nil
// pointer — the latter would defeat mailing.Worker's `if w.progress == nil`
// guard and panic on the first publish.
func TestNewCampaignProgressPublisher_NilBroker_ReturnsGenuinelyNilInterface(t *testing.T) {
	got := NewCampaignProgressPublisher(nil, discardTestLogger())
	if got != nil {
		t.Fatalf("NewCampaignProgressPublisher(nil, ...) = %#v, want a genuinely nil interface", got)
	}
}

// TestCampaignProgressPublisher_PublishesJSONFrameToBroker asserts
// PublishCampaignProgress marshals the CampaignProgress verbatim (including
// its json field names — the frontend contract) and broadcasts it under the
// "campaign.progress" event name via PublishAll (not Publish, which is
// scoped to one userID — campaign progress has no single owner).
func TestCampaignProgressPublisher_PublishesJSONFrameToBroker(t *testing.T) {
	broker := &fakeCampaignProgressBroker{}
	p := &campaignProgressPublisher{broker: broker, log: discardTestLogger()}
	cp := mailing.CampaignProgress{
		CampaignID: 42,
		Status:     "sending",
		Total:      100,
		Sent:       60,
		Failed:     5,
		Skipped:    3,
		Remaining:  32,
	}

	p.PublishCampaignProgress(context.Background(), cp)

	if len(broker.events) != 1 {
		t.Fatalf("broker.events = %d, want exactly 1", len(broker.events))
	}
	got := broker.events[0]
	if got.Name != campaignProgressEventName {
		t.Errorf("event name = %q, want %q", got.Name, campaignProgressEventName)
	}

	// Decoded as `any`, not int64: `status` is a string, and the point of
	// this assertion is the exact wire shape web/src/lib/campaignProgress.ts's
	// CampaignProgress interface is hand-kept in sync with (#0095 — nothing
	// mechanically enforces that agreement; #0093 — two handlers once
	// serialised one concept under two schemas).
	var decoded map[string]any
	if err := json.Unmarshal(got.Payload, &decoded); err != nil {
		t.Fatalf("payload did not decode as JSON: %v", err)
	}
	want := map[string]any{
		"campaign_id": float64(42),
		"status":      "sending",
		"total":       float64(100),
		"sent":        float64(60),
		"failed":      float64(5),
		"skipped":     float64(3),
		"remaining":   float64(32),
	}
	for k, v := range want {
		if decoded[k] != v {
			t.Errorf("payload[%q] = %#v, want %#v (full payload: %s)", k, decoded[k], v, got.Payload)
		}
	}
	// No extra and no missing keys: a field added on the Go side without its
	// TypeScript counterpart is exactly the drift #0095 describes, so the key
	// set is pinned rather than only spot-checked.
	if len(decoded) != len(want) {
		t.Errorf("payload has %d keys, want exactly %d (%s)", len(decoded), len(want), got.Payload)
	}
}

// TestCampaignProgressPublisher_RealBrokerFansOutToAllSubscribers is an
// end-to-end proof (real *events.Broker, no fake) that PublishAll — not
// Publish — is what's used: two DIFFERENT admin userIDs both subscribed
// both receive the frame from one PublishCampaignProgress call.
func TestCampaignProgressPublisher_RealBrokerFansOutToAllSubscribers(t *testing.T) {
	broker := events.NewBroker()
	adminOne := broker.Subscribe(1)
	adminTwo := broker.Subscribe(2)
	defer broker.Unsubscribe(1, adminOne)
	defer broker.Unsubscribe(2, adminTwo)

	pub := NewCampaignProgressPublisher(broker, discardTestLogger())
	if pub == nil {
		t.Fatal("NewCampaignProgressPublisher(real broker, ...) = nil, want non-nil")
	}
	pub.PublishCampaignProgress(context.Background(), mailing.CampaignProgress{CampaignID: 7, Total: 1})

	select {
	case ev := <-adminOne:
		if ev.Name != campaignProgressEventName {
			t.Errorf("admin 1 event name = %q", ev.Name)
		}
	default:
		t.Error("admin 1 (userID 1) received nothing — PublishAll did not reach it")
	}
	select {
	case ev := <-adminTwo:
		if ev.Name != campaignProgressEventName {
			t.Errorf("admin 2 event name = %q", ev.Name)
		}
	default:
		t.Error("admin 2 (userID 2) received nothing — PublishAll did not reach it")
	}
}
