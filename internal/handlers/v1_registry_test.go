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
		"list_outreach", "get_event_outreach", "outreach_connections_status"}
	if !slices.Equal(names, want) {
		t.Fatalf("operations %v, want %v", names, want)
	}
}
