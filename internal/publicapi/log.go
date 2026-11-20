package publicapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// logCall writes one "public api call" record (#0730). No IP, user agent
// or header is logged.
func (r *Registry) logCall(ctx context.Context, op Op, in any, start time.Time, cached bool, err error) {
	l := r.logger
	if l == nil {
		l = slog.Default()
	}
	attrs := []any{
		"tool", op.Name,
		"params", logParams(in),
		"latency_ms", float64(r.now().Sub(start).Microseconds()) / 1000,
		"cached", cached,
	}
	if err != nil {
		code := "internal"
		var e *api.Error
		if errors.As(err, &e) {
			code = e.Code
		}
		attrs = append(attrs, "error", code)
	}
	l.InfoContext(ctx, "public api call", attrs...)
}

// logParams is in as JSON, with a "query" longer than LogQueryRunes runes
// cut there and marked with an ellipsis. nil is "{}"; a failure is "?".
func logParams(in any) string {
	if in == nil {
		return "{}"
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "?"
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "?"
	}
	if m == nil {
		return "{}"
	}
	if q, ok := m["query"].(string); ok {
		if rs := []rune(q); len(rs) > LogQueryRunes {
			m["query"] = string(rs[:LogQueryRunes]) + "…"
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "?"
	}
	return string(out)
}
