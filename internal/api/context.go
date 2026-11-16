package api

import "context"

type clientIPKey struct{}

// WithClientIP returns ctx carrying the caller's IP. The REST adapter sets it
// from middleware.ClientIP for every operation call; #0679's MCP adapter
// should set it too.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIPFromContext is the IP WithClientIP stored, or "". A v1 write puts
// it in its audit entry's IP.
func ClientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}
