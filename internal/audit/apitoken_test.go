package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAttributeAPIToken(t *testing.T) {
	admin := int64(3)
	orig := map[string]any{"slug": "led-basics"}
	e := AttributeAPIToken(Entry{ActorID: &admin, Action: "event.created", Metadata: orig},
		APITokenActor{ID: 9, Name: "Claude Code", Prefix: "ocsf_abcdefg", CreatedBy: &admin})
	if e.ActorID != nil {
		t.Fatalf("ActorID %v, want nil", *e.ActorID)
	}
	if _, ok := orig[MetaActor]; ok {
		t.Fatal("caller's metadata map was mutated")
	}
	b, err := json.Marshal(e.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"actor":"token:9","api_token":{"created_by":3,"id":9,"name":"Claude Code","prefix":"ocsf_abcdefg"},"slug":"led-basics"}`
	if string(b) != want {
		t.Fatalf("metadata\n got %s\nwant %s", b, want)
	}
}

func TestAttributeAPIToken_NilAndNonMapMetadata(t *testing.T) {
	e := AttributeAPIToken(Entry{Action: "x"}, APITokenActor{ID: 1, Name: "n", Prefix: "ocsf_1234567"})
	b, _ := json.Marshal(e.Metadata)
	if !strings.Contains(string(b), `"actor":"token:1"`) || !strings.Contains(string(b), `"created_by":null`) {
		t.Fatalf("nil metadata: %s", b)
	}
	e = AttributeAPIToken(Entry{Action: "x", Metadata: []string{"a"}}, APITokenActor{ID: 2})
	b, _ = json.Marshal(e.Metadata)
	if !strings.Contains(string(b), `"details":["a"]`) {
		t.Fatalf("non-map metadata: %s", b)
	}
}
