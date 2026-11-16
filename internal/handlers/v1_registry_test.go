package handlers

import (
	"slices"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// TestBuildV1RegistryListsCoreAndEngagement (#0675): the one list of v1
// operations, built with nil stores (building never touches a store).
func TestBuildV1RegistryListsCoreAndEngagement(t *testing.T) {
	reg := BuildV1Registry(api.Config{Version: "x"}, V1Deps{})
	var names []string
	for _, op := range reg.Operations() {
		names = append(names, op.Name)
	}
	if want := []string{"whoami", "token_probe", "get_engagement"}; !slices.Equal(names, want) {
		t.Fatalf("operations %v, want %v", names, want)
	}
}
