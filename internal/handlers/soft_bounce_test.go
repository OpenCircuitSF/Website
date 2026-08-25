package handlers

import (
	"context"
	"errors"
	"testing"
)

// fakeSettingsReader is an in-memory softBounceSettingsReader for pure,
// no-DB tests of the threshold-resolution logic itself.
type fakeSettingsReader map[string]string

func (f fakeSettingsReader) GetSetting(ctx context.Context, key string) (string, error) {
	v, ok := f[key]
	if !ok {
		return "", errors.New("fake: setting not found")
	}
	return v, nil
}

func TestSoftBounceThreshold_ReadsConfiguredValue(t *testing.T) {
	reader := fakeSettingsReader{settingSoftBounceThresholdCount: "3"}
	count := softBounceThreshold(context.Background(), reader, nil)
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestSoftBounceThreshold_FallsBackToDefaultWhenMissing(t *testing.T) {
	count := softBounceThreshold(context.Background(), fakeSettingsReader{}, nil)
	if count != defaultSoftBounceThresholdCount {
		t.Errorf("count = %d, want default %d", count, defaultSoftBounceThresholdCount)
	}
}

func TestSoftBounceThreshold_FallsBackOnUnparseableValue(t *testing.T) {
	reader := fakeSettingsReader{settingSoftBounceThresholdCount: "not-a-number"}
	count := softBounceThreshold(context.Background(), reader, nil)
	if count != defaultSoftBounceThresholdCount {
		t.Errorf("count = %d, want default %d on an unparseable value", count, defaultSoftBounceThresholdCount)
	}
}

func TestSoftBounceThreshold_FallsBackOnNonPositiveValue(t *testing.T) {
	for _, v := range []string{"0", "-1", "-5"} {
		reader := fakeSettingsReader{settingSoftBounceThresholdCount: v}
		count := softBounceThreshold(context.Background(), reader, nil)
		if count != defaultSoftBounceThresholdCount {
			t.Errorf("count for value %q = %d, want default %d", v, count, defaultSoftBounceThresholdCount)
		}
	}
}
