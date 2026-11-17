package mcpserver_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
)

// productionRegistry is the registry servePostgres builds. Nil stores are
// fine: these tests list operations and never call one.
func productionRegistry() *api.Registry {
	return handlers.BuildV1Registry(api.Config{Version: "test"}, handlers.V1Deps{})
}

// deliberatelyExcluded is every registered operation that is not an MCP
// tool, each with the reason. TestEveryWriteOperationIsATool fails both
// ways: an excluded operation missing here, or an entry here that is not
// an excluded operation.
var deliberatelyExcluded = map[string]string{
	"token_probe": "deprecated alias of whoami, kept for Home Assistant's REST connection check",
}

// specTools is #0679's requirement 3, each name with the issue that owns its
// operation, plus get_engagement (#0653), which is read-only and costs
// nothing to expose.
var specTools = map[string]string{
	"whoami":                      "#0675",
	"get_engagement":              "#0653",
	"list_guides":                 "#0676",
	"get_guide":                   "#0676",
	"list_guide_posts":            "#0676",
	"create_guide_posts":          "#0676",
	"update_guide_post":           "#0676",
	"schedule_guide_post":         "#0676",
	"unschedule_guide_post":       "#0676",
	"send_guide_post":             "#0676",
	"post_guide_again":            "#0676",
	"delete_guide_post":           "#0676",
	"guide_posts_overview":        "#0676",
	"list_channels":               "#0676",
	"list_events":                 "#0677",
	"get_event":                   "#0677",
	"create_event":                "#0677",
	"update_event":                "#0677",
	"publish_event":               "#0677",
	"unpublish_event":             "#0677",
	"cancel_event":                "#0677",
	"duplicate_event":             "#0677",
	"list_venues":                 "#0677",
	"list_event_types":            "#0677",
	"list_interests":              "#0677",
	"event_coverage":              "#0678",
	"list_cadence_rules":          "#0678",
	"set_cadence_rule":            "#0678",
	"list_outreach":               "#0697",
	"get_event_outreach":          "#0697",
	"outreach_connections_status": "#0697",
	"start_event_outreach":        "#0699",
	"create_outreach_post":        "#0699",
	"update_outreach_post":        "#0699",
	"delete_outreach_post":        "#0699",
	"reset_event_outreach":        "#0699",
	"draft_event_outreach":        "#0700",
	"prepare_outreach_post":       "#0700",
	"ensure_outreach_hand_links":  "#0700",
	"create_media_upload":         "#0705",
	"list_media":                  "#0705",
}

// pendingTools are the spec names whose issue has not landed. The issue that
// adds an operation deletes its name here, or TestMCPToolSetMatchesIssueSpec
// fails. Empty since #0678 landed the last three; a spec tool added before
// its operation exists goes here until it lands.
var pendingTools = []string{}

func TestEveryWriteOperationIsATool(t *testing.T) {
	ops := productionRegistry().Operations()
	excluded := map[string]bool{}
	for _, op := range ops {
		if op.MCPExcluded == "" {
			continue
		}
		excluded[op.Name] = true
		if _, ok := deliberatelyExcluded[op.Name]; !ok {
			kind := "read"
			if op.Method != http.MethodGet {
				kind = "WRITE"
			}
			t.Errorf("%s (%s %s, a %s) is excluded from MCP (%q) but not listed in deliberatelyExcluded", op.Name, op.Method, op.Path, kind, op.MCPExcluded)
		}
	}
	for name := range deliberatelyExcluded {
		if !excluded[name] {
			t.Errorf("deliberatelyExcluded lists %s, which is not an excluded operation; delete the entry", name)
		}
	}
}

func TestMCPToolSetMatchesIssueSpec(t *testing.T) {
	registered := map[string]bool{}
	exposed := map[string]bool{}
	for _, op := range productionRegistry().Operations() {
		registered[op.Name] = true
		if op.MCPExcluded == "" {
			exposed[op.Name] = true
		}
	}
	for _, name := range pendingTools {
		if _, ok := specTools[name]; !ok {
			t.Errorf("pendingTools lists %s, which is not in specTools", name)
		}
		if registered[name] {
			t.Errorf("%s is registered now (%s landed); delete it from pendingTools", name, specTools[name])
		}
	}
	for name, owner := range specTools {
		if !slices.Contains(pendingTools, name) && !exposed[name] {
			t.Errorf("%s (%s) is not pending but is not an exposed tool", name, owner)
		}
	}
	for name := range exposed {
		if _, ok := specTools[name]; !ok {
			t.Errorf("tool %s is in neither specTools nor pendingTools; add it to specTools with its issue", name)
		}
	}
}
