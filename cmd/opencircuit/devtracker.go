package main

import (
	"log/slog"
	"time"
)

// devTrackerInterval returns the outreach tracker's polling interval override
// from OUTREACH_TRACKER_INTERVAL (a Go duration such as "20s"), or 0 for the
// default (#0580). The override exists so the local outreach demo shows
// engagement within seconds rather than after the default fifteen minutes. It
// is honoured only when BASE_URL is loopback, the same gate MAILER_NOOP and
// DEV_ADMIN_LOGIN use, so a production host cannot be made to hammer the
// platforms by a stray variable. Anything below five seconds or unparsable is
// ignored with a warning.
func devTrackerInterval(baseURL, raw string, log *slog.Logger) time.Duration {
	if raw == "" {
		return 0
	}
	if !baseURLIsLoopback(baseURL) {
		log.Warn("OUTREACH_TRACKER_INTERVAL ignored: BASE_URL is not loopback")
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 5*time.Second {
		log.Warn("OUTREACH_TRACKER_INTERVAL ignored: want a duration of at least 5s", "value", raw)
		return 0
	}
	return d
}
