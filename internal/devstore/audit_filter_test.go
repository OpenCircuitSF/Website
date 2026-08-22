// This file is `package devstore` (not devstore_test like the rest of the
// package's tests) because it drives recordAudit directly: in dev mode
// handlers receive a nil *audit.Logger and nothing exported writes an audit
// record, so an external test cannot populate the store to filter over.
package devstore

import (
	"context"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
)

// TestListAuditLog_FilterParity pins the STORAGE=json backend to the same
// audit.Filter semantics audit.Reader.ListAuditLog implements against
// Postgres (#0114) — the AND of whichever fields are set, TargetID ignored
// unless TargetType is also set, and no filter ever touching actor_id so a
// machine-written row (#0045's NULL-actor email_campaign.send_refused) stays
// reachable. Without this, a divergence between the two backends only shows
// up when someone runs the dev server.
func TestListAuditLog_FilterParity(t *testing.T) {
	s := New("")
	ctx := context.Background()
	u1, u2 := int64(11), int64(22)
	c1, c2 := int64(424242), int64(424243)

	// The row #0114 exists to make reachable: no actor, no user.
	s.recordAudit(audit.Entry{Action: audit.ActionEmailCampaignSendRefused,
		TargetType: audit.TargetEmailCampaign, TargetID: &c1})
	// Same type, different campaign.
	s.recordAudit(audit.Entry{Action: audit.ActionEmailCampaignSendRefused,
		TargetType: audit.TargetEmailCampaign, TargetID: &c2})
	// Same id, different type — must never leak into a campaign filter.
	s.recordAudit(audit.Entry{ActorID: &u1, UserID: &u1, Action: audit.ActionSettingsUpdated,
		TargetType: audit.TargetSettings, TargetID: &c1})
	// Same type and id as the first row, but attributed to a user.
	s.recordAudit(audit.Entry{ActorID: &u2, UserID: &u2, Action: audit.ActionEmailCampaignCreated,
		TargetType: audit.TargetEmailCampaign, TargetID: &c1})

	for _, tc := range []struct {
		name string
		f    audit.Filter
		want int64
	}{
		{"no filter", audit.Filter{}, 4},
		{"user_id", audit.Filter{UserID: &u1}, 1},
		{"target_type", audit.Filter{TargetType: audit.TargetEmailCampaign}, 3},
		{"target_id alone is ignored", audit.Filter{TargetID: &c1}, 4},
		{"user_id AND target_type", audit.Filter{UserID: &u2, TargetType: audit.TargetEmailCampaign}, 1},
		{"user_id, target_id ignored", audit.Filter{UserID: &u1, TargetID: &c2}, 1},
		{"target_type AND target_id", audit.Filter{TargetType: audit.TargetEmailCampaign, TargetID: &c1}, 2},
		{"all three", audit.Filter{UserID: &u2, TargetType: audit.TargetEmailCampaign, TargetID: &c1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs, total, err := s.ListAuditLog(ctx, tc.f, 50, 0)
			if err != nil {
				t.Fatalf("ListAuditLog: %v", err)
			}
			if total != tc.want || int64(len(recs)) != tc.want {
				t.Errorf("rows = %d (total %d), want %d", len(recs), total, tc.want)
			}
		})
	}

	// Paging a filtered result: page 2 of the campaign-scoped filter is the
	// older, NULL-actor send_refused row.
	recs, total, err := s.ListAuditLog(ctx,
		audit.Filter{TargetType: audit.TargetEmailCampaign, TargetID: &c1}, 1, 1)
	if err != nil {
		t.Fatalf("ListAuditLog paged: %v", err)
	}
	if total != 2 || len(recs) != 1 {
		t.Fatalf("paged rows = %d (total %d), want 1 of 2", len(recs), total)
	}
	if recs[0].ActorID != nil || recs[0].Action != audit.ActionEmailCampaignSendRefused {
		t.Errorf("page 2 row = actor %v action %q, want the NULL-actor send_refused row",
			recs[0].ActorID, recs[0].Action)
	}
}
