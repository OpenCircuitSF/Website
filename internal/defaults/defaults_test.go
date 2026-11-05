package defaults

import (
	"errors"
	"testing"
)

func TestIsManagedCoversEveryKey(t *testing.T) {
	if len(Keys()) == 0 {
		t.Fatal("Keys() is empty")
	}
	for _, k := range Keys() {
		if !IsManaged(k) {
			t.Errorf("IsManaged(%q) = false", k)
		}
	}
	for _, k := range []string{KeyEventType, KeyVenue} {
		if !IsManaged(k) {
			t.Errorf("IsManaged(%q) = false", k)
		}
	}
	if IsManaged("physical_address") {
		t.Error("IsManaged(physical_address) = true")
	}
}

func TestParseIDRefusesNonCanonicalValues(t *testing.T) {
	for _, raw := range []string{"", "abc", "01", " 1", "1.0"} {
		if _, err := parseID(raw); !errors.Is(err, ErrUnset) {
			t.Errorf("parseID(%q) err = %v, want ErrUnset", raw, err)
		}
	}
	if id, err := parseID("42"); err != nil || id != 42 {
		t.Errorf("parseID(42) = %d, %v", id, err)
	}
}
