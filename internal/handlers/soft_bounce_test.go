package handlers

import (
	"context"
	"errors"
	"testing"
	"time"
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

func TestSoftBounceThreshold_ReadsConfiguredValues(t *testing.T) {
	reader := fakeSettingsReader{
		settingSoftBounceThresholdCount:      "3",
		settingSoftBounceThresholdWindowDays: "7",
	}
	count, window := softBounceThreshold(context.Background(), reader, nil)
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if window != 7*24*time.Hour {
		t.Errorf("window = %v, want 7 days", window)
	}
}

func TestSoftBounceThreshold_FallsBackToDefaultWhenMissing(t *testing.T) {
	count, window := softBounceThreshold(context.Background(), fakeSettingsReader{}, nil)
	if count != defaultSoftBounceThresholdCount {
		t.Errorf("count = %d, want default %d", count, defaultSoftBounceThresholdCount)
	}
	if window != defaultSoftBounceThresholdWindowDays*24*time.Hour {
		t.Errorf("window = %v, want default %d days", window, defaultSoftBounceThresholdWindowDays)
	}
}

func TestSoftBounceThreshold_FallsBackOnUnparseableValue(t *testing.T) {
	reader := fakeSettingsReader{
		settingSoftBounceThresholdCount:      "not-a-number",
		settingSoftBounceThresholdWindowDays: "30",
	}
	count, _ := softBounceThreshold(context.Background(), reader, nil)
	if count != defaultSoftBounceThresholdCount {
		t.Errorf("count = %d, want default %d on an unparseable value", count, defaultSoftBounceThresholdCount)
	}
}

func TestSoftBounceThreshold_FallsBackOnNonPositiveValue(t *testing.T) {
	for _, v := range []string{"0", "-1", "-5"} {
		reader := fakeSettingsReader{settingSoftBounceThresholdCount: v}
		count, _ := softBounceThreshold(context.Background(), reader, nil)
		if count != defaultSoftBounceThresholdCount {
			t.Errorf("count for value %q = %d, want default %d", v, count, defaultSoftBounceThresholdCount)
		}
	}
}
