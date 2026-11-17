package handlers

import (
	"slices"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// TestBuildV1RegistryListsOperations (#0675): the one list of v1
// operations, built with nil stores (building never touches a store).
func TestBuildV1RegistryListsOperations(t *testing.T) {
	reg := BuildV1Registry(api.Config{Version: "x"}, V1Deps{})
	var names []string
	for _, op := range reg.Operations() {
		names = append(names, op.Name)
	}
	want := []string{"whoami", "token_probe", "get_engagement",
		"list_guides", "get_guide", "list_guide_posts", "create_guide_posts", "update_guide_post",
		"schedule_guide_post", "unschedule_guide_post", "send_guide_post", "post_guide_again",
		"delete_guide_post", "guide_posts_overview", "list_channels",
		"list_events", "get_event", "create_event", "update_event", "publish_event", "unpublish_event",
		"cancel_event", "duplicate_event", "list_venues", "list_event_types", "list_interests",
		"event_coverage", "list_cadence_rules", "set_cadence_rule",
		"list_outreach", "get_event_outreach", "outreach_connections_status",
		"start_event_outreach", "create_outreach_post", "update_outreach_post", "delete_outreach_post",
		"reset_event_outreach", "draft_event_outreach", "prepare_outreach_post", "ensure_outreach_hand_links",
		"schedule_outreach_wave", "unschedule_outreach_wave", "send_outreach_post", "mark_outreach_post_posted",
		"unmark_outreach_post_posted", "takedown_outreach_post",
		"create_media_upload", "list_media"}
	if !slices.Equal(names, want) {
		t.Fatalf("operations %v, want %v", names, want)
	}
}
