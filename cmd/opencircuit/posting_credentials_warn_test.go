package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
)

func TestWarnPartialPostingCredentials_Whitespace(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want []string // substrings that must appear
		none bool     // nothing may be logged
	}{
		{name: "all unset", cfg: config.Config{}, none: true},
		{name: "all set", cfg: config.Config{BlueskyHandle: "h", BlueskyAppPassword: "p", MastodonBaseURL: "u", MastodonToken: "t"}, none: true},
		{name: "whitespace password", cfg: config.Config{BlueskyHandle: "h", BlueskyAppPassword: "  \t"},
			want: []string{"BLUESKY_APP_PASSWORD is set but contains only whitespace", "BLUESKY_APP_PASSWORD is missing"}},
		{name: "whitespace token", cfg: config.Config{MastodonBaseURL: "https://m.example.test", MastodonToken: " "},
			want: []string{"MASTODON_TOKEN is set but contains only whitespace", "MASTODON_TOKEN is missing"}},
		{name: "whitespace alone", cfg: config.Config{BlueskyHandle: " "},
			want: []string{"BLUESKY_HANDLE is set but contains only whitespace"}},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		warnPartialPostingCredentials(&tc.cfg, slog.New(slog.NewTextHandler(&buf, nil)))
		out := buf.String()
		if tc.none && out != "" {
			t.Errorf("%s: unexpected log %q", tc.name, out)
		}
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: log %q lacks %q", tc.name, out, w)
			}
		}
	}
}
