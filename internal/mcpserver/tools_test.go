package mcpserver_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
)

// TestToolMirrorsOperation: a tool is its operation, field for field.
func TestToolMirrorsOperation(t *testing.T) {
	ops := append(api.CoreOperations("v"), testOps(&recorder{})...)
	for _, op := range ops {
		tool := mcpserver.ToolFor(op)
		wantDesc := op.Describe()
		if op.Idempotency {
			wantDesc += mcpserver.IdempotencyKeyNote
		}
		if tool.Name != op.Name || tool.Description != wantDesc {
			t.Errorf("%s: name/description %q / %q", op.Name, tool.Name, tool.Description)
		}
		if !reflect.DeepEqual(withoutIdempotencyKey(t, op, tool.InputSchema), op.InputSchema()) {
			t.Errorf("%s: input schema differs from the registry's", op.Name)
		}
		if !reflect.DeepEqual(tool.OutputSchema, op.OutputSchema()) {
			t.Errorf("%s: output schema differs from the registry's", op.Name)
		}
		a := tool.Annotations
		if a == nil {
			t.Errorf("%s: no annotations", op.Name)
			continue
		}
		if a.Title != op.Summary {
			t.Errorf("%s: title %q, want the summary", op.Name, a.Title)
		}
		if a.ReadOnlyHint != (op.Method == http.MethodGet) {
			t.Errorf("%s: ReadOnlyHint %v for %s", op.Name, a.ReadOnlyHint, op.Method)
		}
		if a.IdempotentHint != (op.Method == http.MethodGet || op.Method == http.MethodDelete) {
			t.Errorf("%s: IdempotentHint %v for %s", op.Name, a.IdempotentHint, op.Method)
		}
		if a.DestructiveHint == nil || *a.DestructiveHint != op.Destructive {
			t.Errorf("%s: DestructiveHint %v, want %v", op.Name, a.DestructiveHint, op.Destructive)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint != (op.PublicEffect != "") {
			t.Errorf("%s: OpenWorldHint %v, want %v", op.Name, a.OpenWorldHint, op.PublicEffect != "")
		}
	}
}

// TestDescriptionsLeadWithWarnings: a client that asks before acting sees
// the warning first, and every description ends with the scope it needs.
func TestDescriptionsLeadWithWarnings(t *testing.T) {
	sawDestructive, sawPublic := false, false
	for _, op := range testOps(&recorder{}) {
		d := strings.TrimSuffix(mcpserver.ToolFor(op).Description, mcpserver.IdempotencyKeyNote)
		if op.Destructive {
			sawDestructive = true
			if !strings.HasPrefix(d, "DESTRUCTIVE: ") {
				t.Errorf("%s: %q does not lead with DESTRUCTIVE", op.Name, d)
			}
		}
		if !op.Destructive && op.PublicEffect != "" {
			sawPublic = true
			if !strings.HasPrefix(d, "PUBLIC: ") {
				t.Errorf("%s: %q does not lead with PUBLIC", op.Name, d)
			}
		}
		if !strings.HasSuffix(d, "Requires the "+op.Scope+" scope.") {
			t.Errorf("%s: %q does not end with its scope", op.Name, d)
		}
	}
	if !sawDestructive || !sawPublic {
		t.Fatal("the fixture lacks a destructive or a public operation")
	}
}
