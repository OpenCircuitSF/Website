package publicmcp

// The MCP Server Card and AI Catalog (#0731), per SEP-2127 "MCP Server
// Cards" (Final, https://modelcontextprotocol.io/seps/2127-mcp-server-cards)
// and the wire format in modelcontextprotocol/experimental-ext-server-card
// at main 28db12c9 (its schema.ts and its discovery doc), read 2026-11-19.
//
// A card carries connection details only: the spec leaves tools, resources
// and prompts out on purpose, and "no auth" is a remote with no required
// headers. Both documents are built once from the same constants and
// registry version the live server uses, so they cannot drift from it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
)

const (
	// CardPath is the spec's reserved card location, <streamable-http-url>/server-card.
	CardPath = Path + "/server-card"
	// WellKnownCardAlias serves the same card bytes. It is not part of the
	// spec; this issue asked for it by name.
	WellKnownCardAlias = "/.well-known/mcp.json"
	// CatalogPath is the spec's domain-level discovery document.
	CatalogPath = "/.well-known/ai-catalog.json"

	CardSchema       = "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json"
	CardName         = "com.opencircuitsf/" + ServerName
	CardDescription  = "Read-only Open Circuit SF events and newsletter issues. No account or key needed."
	CardMediaType    = "application/mcp-server-card+json"
	CatalogMediaType = "application/ai-catalog+json"
)

type serverCard struct {
	Schema      string       `json:"$schema"`
	Name        string       `json:"name"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	WebsiteURL  string       `json:"websiteUrl,omitempty"`
	Icons       []cardIcon   `json:"icons,omitempty"`
	Remotes     []cardRemote `json:"remotes,omitempty"`
}

type cardIcon struct {
	Src      string   `json:"src"`
	MimeType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
}

type cardRemote struct {
	Type                      string   `json:"type"`
	URL                       string   `json:"url"`
	SupportedProtocolVersions []string `json:"supportedProtocolVersions,omitempty"`
}

type catalog struct {
	SpecVersion string         `json:"specVersion"`
	Entries     []catalogEntry `json:"entries"`
}

type catalogEntry struct {
	Identifier string `json:"identifier"`
	Type       string `json:"type"`
	URL        string `json:"url"`
}

// BuildServerCard is the v1 server card for version at baseURL.
func BuildServerCard(version, baseURL string) ([]byte, error) {
	base := strings.TrimRight(baseURL, "/")
	return json.Marshal(serverCard{
		Schema:      CardSchema,
		Name:        CardName,
		Title:       ServerTitle,
		Description: CardDescription,
		Version:     version,
		WebsiteURL:  base + DocsPath,
		Icons:       []cardIcon{{Src: base + "/mark-green-256.png", MimeType: "image/png", Sizes: []string{"256x256"}}},
		Remotes: []cardRemote{{
			Type:                      "streamable-http",
			URL:                       base + Path,
			SupportedProtocolVersions: mcp.SupportedProtocolVersions(),
		}},
	})
}

// buildCatalog is the AI Catalog with one entry pointing at the card.
func buildCatalog(baseURL string) ([]byte, error) {
	base := strings.TrimRight(baseURL, "/")
	return json.Marshal(catalog{
		SpecVersion: "1.0",
		Entries: []catalogEntry{{
			Identifier: "urn:air:opencircuitsf.com:mcp:" + ServerName,
			Type:       CardMediaType,
			URL:        base + CardPath,
		}},
	})
}

// staticJSON serves body with the spec's CORS and caching headers and an
// ETag that honours If-None-Match.
func staticJSON(body []byte, mediaType string) http.Handler {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:])[:16] + `"`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET")
			h.Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.Set("Cache-Control", "public, max-age=3600")
		h.Set("ETag", etag)
		if inm := r.Header.Get("If-None-Match"); inm == etag || inm == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		h.Set("Content-Type", mediaType)
		_, _ = w.Write(body)
	})
}

// MountDiscovery registers the card at CardPath and WellKnownCardAlias and
// the catalog at CatalogPath, GET and OPTIONS only. Call it once.
func MountDiscovery(mux *http.ServeMux, reg *publicapi.Registry, baseURL string) {
	card, err := BuildServerCard(reg.Version(), baseURL)
	if err != nil {
		panic("publicmcp: server card: " + err.Error())
	}
	cat, err := buildCatalog(baseURL)
	if err != nil {
		panic("publicmcp: ai catalog: " + err.Error())
	}
	cardH := staticJSON(card, CardMediaType)
	catH := staticJSON(cat, CatalogMediaType)
	for _, m := range []string{http.MethodGet, http.MethodOptions} {
		mux.Handle(m+" "+CardPath, cardH)
		mux.Handle(m+" "+WellKnownCardAlias, cardH)
		mux.Handle(m+" "+CatalogPath, catH)
	}
}
