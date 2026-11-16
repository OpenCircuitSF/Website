package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// MaxBodyBytes caps a v1 request body.
const MaxBodyBytes = 64 << 10

// RouterDeps is what NewHTTPRouter needs besides the registry.
type RouterDeps struct {
	Resolver middleware.APITokenResolver
	// Limiter is the per-token limiter (middleware.NewAPITokenRateLimiter).
	// Share one instance with #0679's /mcp so a key has one budget.
	Limiter *middleware.RateLimiter
	BaseURL string // the OpenAPI servers entry
}

// Methods are the HTTP methods a v1 operation may use, and the ones Mount
// routes to the v1 router.
var Methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// Mount registers h, the router NewHTTPRouter returns, for every v1 path on
// mux: one "<METHOD> /api/v1/" pattern per entry in Methods. A bare
// "/api/v1/" pattern cannot be used, because it conflicts with the SPA's
// "GET /" catch-all in Go's ServeMux. Call it once.
func Mount(mux *http.ServeMux, h http.Handler) {
	for _, m := range Methods {
		mux.Handle(m+" /api/v1/", h)
	}
}

// NewHTTPRouter freezes reg and returns the /api/v1/ handler; mount it once
// with Mount. It serves
//
//   - GET OpenAPIPath, public, the document built from reg;
//   - each operation at its method and path, behind RequireAPIToken, the
//     per-token rate limit and the operation's scope check, in that order
//     (authentication is per route, so the document and the 404 stay public);
//   - a JSON 405 with Allow for a known path with the wrong method, and a
//     JSON 404 for anything else under /api/v1/.
//
// It panics on a wiring bug: a nil resolver or limiter, an operation that
// takes an Idempotency-Key with no IdempotencyStore configured, or an OpenAPI
// build failure.
func NewHTTPRouter(reg *Registry, deps RouterDeps) http.Handler {
	if deps.Resolver == nil || deps.Limiter == nil {
		panic("api: NewHTTPRouter needs a Resolver and a Limiter")
	}
	reg.Freeze()
	doc, err := buildOpenAPI(reg, deps.BaseURL)
	if err != nil {
		panic("api: OpenAPI document: " + err.Error())
	}
	inner := http.NewServeMux()
	inner.HandleFunc("GET "+OpenAPIPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(doc)
	})
	auth := middleware.RequireAPIToken(deps.Resolver, "")
	limit := middleware.APITokenRateLimit(deps.Limiter)
	for _, op := range reg.Operations() {
		if op.Idempotency && reg.cfg.Idempotency == nil {
			panic("api: " + op.Name + " takes an Idempotency-Key but the registry has no IdempotencyStore")
		}
		inner.Handle(op.Method+" "+op.Path, auth(limit(requireScope(op, serve(op, reg.cfg.Idempotency, reg.cfg.Now)))))
	}
	inner.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var allow []string
		for _, m := range Methods {
			probe := r.Clone(r.Context())
			probe.Method = m
			if _, pat := inner.Handler(probe); pat != "" && pat != "/api/v1/" {
				allow = append(allow, m)
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			writeError(w, &Error{Status: http.StatusMethodNotAllowed, Code: CodeMethodNotAllowed, Message: "this endpoint does not accept " + r.Method})
			return
		}
		writeError(w, &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: "no such API endpoint"})
	})
	return inner
}

// requireScope answers 403 unless the request's token satisfies op's scope.
// RequireAPIToken (mounted with "") has already put the token in context.
func requireScope(op Operation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := middleware.APITokenFromContext(r.Context())
		if !ok {
			writeError(w, &Error{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "a valid API key is required"})
			return
		}
		if !op.AnyToken && !apitokens.ScopeSatisfies(tok.Scopes, op.Scope) {
			writeError(w, Forbidden(op.Scope))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve is the HTTP adapter for one operation with no authentication and no
// idempotency: decode, call, encode, map errors. Unit tests use it; the
// mounted routes come from NewHTTPRouter.
func Serve(op Operation) http.Handler { return serve(op, nil, time.Now) }

func serve(op Operation, idem IdempotencyStore, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		r = r.WithContext(WithClientIP(r.Context(), middleware.ClientIP(r)))
		body, e := readBody(w, r)
		if e != nil {
			writeError(w, e)
			return
		}
		in, e := bind(op, body, r.PathValue, r.URL.Query())
		if e != nil {
			writeError(w, e)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		tok, hasTok := middleware.APITokenFromContext(r.Context())
		if !op.Idempotency || key == "" || idem == nil || !hasTok {
			status, payload, e := run(r.Context(), op, in)
			if e != nil {
				writeError(w, e)
				return
			}
			writeRaw(w, status, payload)
			return
		}
		if !ValidIdempotencyKey(key) {
			writeError(w, Validation(map[string]string{"Idempotency-Key": "must be 1 to 255 printable ASCII characters"}))
			return
		}
		sum := sha256.Sum256([]byte(op.Name + "\n" + r.URL.Path + "\n" + r.URL.RawQuery + "\n" + string(body)))
		status, payload, replayed, e := RunIdempotent(r.Context(), idem, now(), tok.ID, key, op.Name, sum[:], func(ctx context.Context) (int, []byte, *Error) {
			return run(ctx, op, in)
		})
		if e != nil {
			writeError(w, e)
			return
		}
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		writeRaw(w, status, payload)
	})
}

// RunIdempotent runs fn under an idempotency key, for either adapter: REST's
// serve and #0685's MCP tools. The key is claimed per API key (tokenID) for
// this operation and request hash. A key already used with a different
// request, or still in flight, is a 409 Conflict. A completed key replays
// its stored status and body (replayed is true) without running fn.
// Otherwise fn runs: a success is stored, and an error forgets the key so a
// retry runs again. Abandon and Complete are detached from ctx, so a
// cancelled request still releases or records its key. Check the key with
// ValidIdempotencyKey first; each adapter names the field in its own terms.
func RunIdempotent(ctx context.Context, idem IdempotencyStore, now time.Time, tokenID int64, key, op string, hash []byte,
	fn func(ctx context.Context) (int, []byte, *Error)) (status int, body []byte, replayed bool, e *Error) {
	stored, err := idem.Begin(ctx, tokenID, key, op, hash, now)
	switch {
	case errors.Is(err, ErrIdempotencyMismatch):
		return 0, nil, false, Conflict("Idempotency-Key was used with a different request")
	case errors.Is(err, ErrIdempotencyInFlight):
		return 0, nil, false, Conflict("a request with this Idempotency-Key is still in progress")
	case err != nil:
		return 0, nil, false, AsError(op, err)
	case stored != nil:
		return stored.Status, stored.Body, true, nil
	}
	status, body, e = fn(ctx)
	if e != nil {
		if err := idem.Abandon(context.WithoutCancel(ctx), tokenID, key); err != nil {
			slog.Error("api: idempotency abandon failed", "op", op, "err", err)
		}
		return 0, nil, false, e
	}
	if err := idem.Complete(context.WithoutCancel(ctx), tokenID, key, status, body); err != nil {
		slog.Error("api: idempotency complete failed", "op", op, "err", err)
	}
	return status, body, false, nil
}

// CallContext is the context op runs on: the one cancellation policy both
// adapters share (#0685). A read (GET) runs on ctx, so a client that gives
// up cancels it. A write runs on context.WithoutCancel(ctx): once it starts,
// a create, send or publish finishes even if the client disconnects. It is
// never left half done, and an idempotent write records its result for the
// retry to replay. Context values (the token, the client IP) are kept
// either way.
func CallContext(ctx context.Context, op Operation) context.Context {
	if op.Method == http.MethodGet {
		return ctx
	}
	return context.WithoutCancel(ctx)
}

// readBody reads a POST, PUT or PATCH body, capped at MaxBodyBytes. A
// non-empty body must be application/json.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, *Error) {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return nil, nil
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, &Error{Status: http.StatusRequestEntityTooLarge, Code: CodeValidation, Message: "the request body is too large",
				Fields: map[string]string{"body": "must be at most 64 KB"}}
		}
		return nil, Validation(map[string]string{"body": "could not be read"})
	}
	if len(bytes.TrimSpace(b)) > 0 {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			return nil, &Error{Status: http.StatusUnsupportedMediaType, Code: CodeValidation, Message: "the request body must be JSON",
				Fields: map[string]string{"Content-Type": "must be application/json"}}
		}
	}
	return b, nil
}

// run calls op and encodes its result, as handlers' writeJSON does (an
// Encoder, so HTML-escaped with a trailing newline): the engagement success
// body stays byte-identical across the move onto the registry.
func run(ctx context.Context, op Operation, in any) (int, []byte, *Error) {
	out, err := op.call(CallContext(ctx, op), in)
	if err != nil {
		return 0, nil, AsError(op.Name, err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(out); err != nil {
		return 0, nil, AsError(op.Name, err)
	}
	status := op.Status
	if status == 0 {
		status = http.StatusOK
	}
	return status, buf.Bytes(), nil
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, e *Error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e.Body())
}

// ValidIdempotencyKey reports whether k is 1 to 255 printable ASCII
// characters: the rule for the Idempotency-Key header and for MCP's
// idempotency_key argument.
func ValidIdempotencyKey(k string) bool {
	if len(k) < 1 || len(k) > 255 {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x20 || k[i] > 0x7e {
			return false
		}
	}
	return true
}
