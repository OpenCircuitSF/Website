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
		if !validIdempotencyKey(key) {
			writeError(w, Validation(map[string]string{"Idempotency-Key": "must be 1 to 255 printable ASCII characters"}))
			return
		}
		sum := sha256.Sum256([]byte(op.Name + "\n" + r.URL.Path + "\n" + string(body)))
		stored, err := idem.Begin(r.Context(), tok.ID, key, op.Name, sum[:], now())
		switch {
		case errors.Is(err, ErrIdempotencyMismatch):
			writeError(w, Conflict("Idempotency-Key was used with a different request"))
			return
		case errors.Is(err, ErrIdempotencyInFlight):
			writeError(w, Conflict("a request with this Idempotency-Key is still in progress"))
			return
		case err != nil:
			writeError(w, AsError(op.Name, err))
			return
		case stored != nil:
			w.Header().Set("Idempotent-Replayed", "true")
			writeRaw(w, stored.Status, stored.Body)
			return
		}
		status, payload, e := run(r.Context(), op, in)
		if e != nil {
			// Detached: a cancelled request must still release its key.
			if err := idem.Abandon(context.WithoutCancel(r.Context()), tok.ID, key); err != nil {
				slog.Error("api: idempotency abandon failed", "op", op.Name, "err", err)
			}
			writeError(w, e)
			return
		}
		if err := idem.Complete(context.WithoutCancel(r.Context()), tok.ID, key, status, payload); err != nil {
			slog.Error("api: idempotency complete failed", "op", op.Name, "err", err)
		}
		writeRaw(w, status, payload)
	})
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
	out, err := op.call(ctx, in)
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

// validIdempotencyKey: 1 to 255 printable ASCII characters.
func validIdempotencyKey(k string) bool {
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
