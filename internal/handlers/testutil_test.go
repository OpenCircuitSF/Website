package handlers

import "strconv"

// itoa is a tiny int64→string helper for building URL paths in tests. Ported
// alongside clientIP (request.go) from the deleted
// internal/handlers/url_filters_test.go — several retained tests
// (users_test.go, audit_test.go) build admin URL paths with it.
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
