package main

import (
	"log/slog"
	"testing"
	"time"
)

func TestDevTrackerInterval(t *testing.T) {
	log := slog.Default()
	cases := []struct {
		base, raw string
		want      time.Duration
	}{
		{"http://localhost:18080", "20s", 20 * time.Second},
		{"http://127.0.0.1:8080", "1m", time.Minute},
		{"http://localhost:18080", "", 0},
		{"http://localhost:18080", "1s", 0},
		{"http://localhost:18080", "soon", 0},
		{"https://www.opencircuitsf.com", "20s", 0},
	}
	for _, c := range cases {
		if got := devTrackerInterval(c.base, c.raw, log); got != c.want {
			t.Errorf("devTrackerInterval(%q, %q) = %v, want %v", c.base, c.raw, got, c.want)
		}
	}
}
