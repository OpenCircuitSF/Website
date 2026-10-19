// Package devmocks is a development-only stand-in for the three services the
// outreach feature talks to: Bluesky (atproto XRPC), Mastodon and ShortLinks
// (#0580). One handler serves all three under path prefixes (/bluesky,
// /mastodon, /shortlinks) so a local dev server can run every outreach screen
// end to end with no network and no real accounts.
//
// Only cmd/devmocks imports this package. It is not part of the production
// binary, and scripts/deploy.sh does not install cmd/devmocks.
//
// State is in memory. Every service accepts any token or app password except
// BadToken, which answers 401 so the Test connection failure path can be shown.
package devmocks

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BadToken is the magic credential value every mock rejects with a 401.
const BadToken = "bad-token"

// RequireLoopback returns an error unless addr is a host:port whose host is a
// loopback IP literal or "localhost". The mocks never listen anywhere else.
func RequireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("devmocks: bad listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("devmocks: refusing to listen on %q: loopback addresses only", addr)
}

// Server holds the in-memory state of all three mocks.
type Server struct {
	public string // externally visible base URL, no trailing slash
	now    func() time.Time
	logf   func(format string, args ...any)

	mu sync.Mutex
	// Bluesky.
	bsPosts map[string]*bsPost // by rkey
	bsNotes []bsNote           // newest first
	bsSeq   int
	// Mastodon.
	maStatuses []*maStatus // newest first
	maByKey    map[string]*maStatus
	maNotes    []maNote // newest first
	maSeq      int64
	// ShortLinks.
	slCampaigns map[string]*slCampaign
	slLinks     map[string]*slLink
	slOrder     []string
	slSeq       int64
}

// New builds a Server. public is the base URL clients reach it at, used in
// the ids and links it hands back. logf receives one line per request; nil
// logs to the standard logger.
func New(public string, now func() time.Time, logf func(string, ...any)) *Server {
	if now == nil {
		now = time.Now
	}
	if logf == nil {
		logf = log.Printf
	}
	s := &Server{
		public: strings.TrimRight(public, "/"), now: now, logf: logf,
		bsPosts: map[string]*bsPost{}, maByKey: map[string]*maStatus{},
		slCampaigns: map[string]*slCampaign{}, slLinks: map[string]*slLink{},
		maSeq: 115000000000000000, slSeq: 100,
	}
	s.seedMentions()
	return s
}

func hash(parts ...string) uint64 {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return binary.BigEndian.Uint64(h[:8])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bearer(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if len(a) > 7 && strings.EqualFold(a[:7], "bearer ") {
		return strings.TrimSpace(a[7:])
	}
	return ""
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(c int) { r.code = c; r.ResponseWriter.WriteHeader(c) }

// Handler returns the combined handler. Request lines are logged without any
// header, so no token ever reaches the log.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	s.routeBluesky(mux)
	s.routeMastodon(mux)
	s.routeShortLinks(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		mux.ServeHTTP(rec, r)
		s.logf("devmocks %s %s -> %d", r.Method, r.URL.RequestURI(), rec.code)
	})
}

func readBody(r *http.Request) []byte {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	return b
}

func ts(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// ---------------------------------------------------------------- Bluesky

const bsDID = "did:plc:mockopencircuitsf"

type bsPost struct {
	URI, CID, RKey, Text, CreatedAt string
	Likes, Reposts, Replies, Quotes int
}

type bsNote struct {
	URI, Reason, Subject, Author, Text, At, ParentURI string
}

func (s *Server) seedMentions() {
	now := s.now()
	s.bsNotes = append(s.bsNotes, bsNote{
		URI: "at://did:plc:mockgrace/app.bsky.feed.post/3mockmention", Reason: "mention",
		Author: "grace.example.test", Text: "@opencircuitsf are you running the LED workshop again this month?",
		At: ts(now.Add(-2 * time.Hour)),
	})
	s.maSeq++
	s.maNotes = append(s.maNotes, s.maReplyNote(nil, "mention", "hopper@hachyderm.example", "Love what Open Circuit SF is doing with the soldering nights.", now.Add(-3*time.Hour)))
}

func (s *Server) routeBluesky(mux *http.ServeMux) {
	p := "/bluesky/xrpc/"
	session := func(w http.ResponseWriter, identifier string) {
		s.mu.Lock()
		s.bsSeq++
		n := s.bsSeq
		s.mu.Unlock()
		writeJSON(w, 200, map[string]string{
			"accessJwt": "mock-access-" + strconv.Itoa(n), "refreshJwt": "mock-refresh-" + strconv.Itoa(n),
			"did": bsDID, "handle": identifier,
		})
	}
	authErr := func(w http.ResponseWriter) {
		writeJSON(w, 401, map[string]string{"error": "AuthenticationRequired", "message": "Invalid identifier or password"})
	}
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if !strings.HasPrefix(bearer(r), "mock-access-") {
			writeJSON(w, 401, map[string]string{"error": "AuthenticationRequired", "message": "Authentication Required"})
			return false
		}
		return true
	}
	mux.HandleFunc("POST "+p+"com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Identifier, Password string }
		_ = json.Unmarshal(readBody(r), &in)
		if in.Password == "" || in.Password == BadToken {
			authErr(w)
			return
		}
		session(w, in.Identifier)
	})
	mux.HandleFunc("POST "+p+"com.atproto.server.refreshSession", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(bearer(r), "mock-refresh-") {
			authErr(w)
			return
		}
		session(w, "opencircuitsf.demo")
	})
	mux.HandleFunc("GET "+p+"com.atproto.repo.getRecord", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		post := s.bsPosts[r.URL.Query().Get("rkey")]
		s.mu.Unlock()
		if post == nil {
			writeJSON(w, 400, map[string]string{"error": "RecordNotFound", "message": "Could not locate record"})
			return
		}
		writeJSON(w, 200, map[string]any{"uri": post.URI, "cid": post.CID, "value": map[string]string{"text": post.Text}})
	})
	mux.HandleFunc("POST "+p+"com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var in struct {
			Repo, Collection, RKey string
			Record                 struct{ Text, CreatedAt string }
		}
		_ = json.Unmarshal(readBody(r), &in)
		if in.RKey == "" {
			writeJSON(w, 400, map[string]string{"error": "InvalidRequest", "message": "rkey required"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if old := s.bsPosts[in.RKey]; old != nil {
			writeJSON(w, 200, map[string]string{"uri": old.URI, "cid": old.CID})
			return
		}
		h := hash("bs", in.RKey)
		post := &bsPost{
			URI: "at://" + bsDID + "/app.bsky.feed.post/" + in.RKey, CID: fmt.Sprintf("bafyreimock%012x", h&0xffffffffffff),
			RKey: in.RKey, Text: in.Record.Text, CreatedAt: in.Record.CreatedAt,
			Likes: 3 + int(h%9), Reposts: int(h/9) % 3, Replies: 1,
		}
		s.bsPosts[in.RKey] = post
		s.bsNotes = append([]bsNote{{
			URI: "at://did:plc:mockada/app.bsky.feed.post/3reply" + in.RKey, Reason: "reply", Subject: post.URI,
			Author: "ada.example.test", Text: "Count me in! Is there parking nearby?", At: ts(s.now()), ParentURI: post.URI,
		}}, s.bsNotes...)
		writeJSON(w, 200, map[string]string{"uri": post.URI, "cid": post.CID})
	})
	mux.HandleFunc("POST "+p+"com.atproto.repo.uploadBlob", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		b := readBody(r)
		writeJSON(w, 200, map[string]any{"blob": map[string]any{
			"$type": "blob", "ref": map[string]string{"$link": fmt.Sprintf("bafkreimock%010x", hash(string(b))&0xffffffffff)},
			"mimeType": r.Header.Get("Content-Type"), "size": len(b),
		}})
	})
	mux.HandleFunc("GET "+p+"app.bsky.feed.getPosts", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		posts := []map[string]any{}
		for _, u := range r.URL.Query()["uris"] {
			if post := s.bsPosts[u[strings.LastIndex(u, "/")+1:]]; post != nil && post.URI == u {
				posts = append(posts, map[string]any{
					"uri": post.URI, "cid": post.CID, "likeCount": post.Likes, "repostCount": post.Reposts,
					"replyCount": post.Replies, "quoteCount": post.Quotes,
				})
			}
		}
		writeJSON(w, 200, map[string]any{"posts": posts})
	})
	mux.HandleFunc("GET "+p+"app.bsky.notification.listNotifications", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []map[string]any{}
		for _, n := range s.bsNotes {
			rec := map[string]any{"text": n.Text, "createdAt": n.At}
			if n.ParentURI != "" {
				ref := map[string]string{"uri": n.ParentURI}
				rec["reply"] = map[string]any{"parent": ref, "root": ref}
			}
			out = append(out, map[string]any{
				"uri": n.URI, "reason": n.Reason, "reasonSubject": n.Subject, "indexedAt": n.At,
				"author": map[string]string{"handle": n.Author}, "record": rec,
			})
		}
		writeJSON(w, 200, map[string]any{"notifications": out})
	})
	mux.HandleFunc("GET /bluesky/profile/{handle}/post/{rkey}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		post := s.bsPosts[r.PathValue("rkey")]
		s.mu.Unlock()
		s.postPage(w, "Bluesky", post != nil, func() string { return post.Text })
	})
}

func (s *Server) postPage(w http.ResponseWriter, svc string, found bool, text func() string) {
	if !found {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><title>Mock %s post</title><body style=\"font-family:sans-serif;max-width:40em;margin:2em auto\"><p><small>Mock %s post (development only)</small></p><p style=\"white-space:pre-wrap\">%s</p>", svc, svc, html.EscapeString(text()))
}

// --------------------------------------------------------------- Mastodon

type maStatus struct {
	ID, URL, URI, Text, CreatedAt  string
	Favs, Reblogs, Replies, Quotes int
}

type maNote struct {
	ID, Type, Acct, At, StatusID, StatusURL, StatusURI, ReplyTo, Content string
}

func (s *Server) maURL(id string) string { return s.public + "/mastodon/@opencircuitsf/" + id }

// maReplyNote builds a notification for a fresh remote status; the caller
// holds the lock or is the constructor.
func (s *Server) maReplyNote(to *maStatus, typ, acct, text string, at time.Time) maNote {
	s.maSeq++
	id := strconv.FormatInt(s.maSeq, 10)
	n := maNote{
		ID: id, Type: typ, Acct: acct, At: ts(at), StatusID: id,
		StatusURL: s.public + "/mastodon/@" + strings.SplitN(acct, "@", 2)[0] + "/" + id,
		StatusURI: s.public + "/mastodon/users/" + strings.SplitN(acct, "@", 2)[0] + "/statuses/" + id,
		Content:   "<p>" + html.EscapeString(text) + "</p>",
	}
	if to != nil {
		n.ReplyTo = to.ID
	}
	return n
}

func (s *Server) routeMastodon(mux *http.ServeMux) {
	p := "/mastodon/api/"
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if t := bearer(r); t == "" || t == BadToken {
			writeJSON(w, 401, map[string]string{"error": "The access token is invalid"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET "+p+"v1/accounts/verify_credentials", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			writeJSON(w, 200, map[string]string{"id": "1", "acct": "opencircuitsf", "username": "opencircuitsf", "url": s.public + "/mastodon/@opencircuitsf"})
		}
	})
	mux.HandleFunc("POST "+p+"v1/statuses", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var in struct{ Status string }
		_ = json.Unmarshal(readBody(r), &in)
		if strings.TrimSpace(in.Status) == "" {
			writeJSON(w, 422, map[string]string{"error": "Validation failed: Text can't be blank"})
			return
		}
		key := r.Header.Get("Idempotency-Key")
		s.mu.Lock()
		defer s.mu.Unlock()
		if st := s.maByKey[key]; key != "" && st != nil {
			writeJSON(w, 200, s.maJSON(st))
			return
		}
		s.maSeq++
		id := strconv.FormatInt(s.maSeq, 10)
		h := hash("ma", id)
		st := &maStatus{
			ID: id, URL: s.maURL(id), URI: s.public + "/mastodon/users/opencircuitsf/statuses/" + id, Text: in.Status,
			CreatedAt: ts(s.now()), Favs: 4 + int(h%8), Reblogs: int(h/8) % 3, Replies: 1,
		}
		s.maStatuses = append([]*maStatus{st}, s.maStatuses...)
		if key != "" {
			s.maByKey[key] = st
		}
		s.maNotes = append([]maNote{s.maReplyNote(st, "mention", "grace@hachyderm.example", "Sounds great, see you there!", s.now())}, s.maNotes...)
		writeJSON(w, 200, s.maJSON(st))
	})
	mux.HandleFunc("POST "+p+"v2/media", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			b := readBody(r)
			writeJSON(w, 200, map[string]any{"id": strconv.FormatUint(hash(string(b))%1e9, 10), "type": "image", "url": s.public + "/mastodon/media/mock.png"})
		}
	})
	mux.HandleFunc("GET "+p+"v1/statuses/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, st := range s.maStatuses {
			if st.ID == r.PathValue("id") {
				writeJSON(w, 200, s.maJSON(st))
				return
			}
		}
		writeJSON(w, 404, map[string]string{"error": "Record not found"})
	})
	mux.HandleFunc("GET "+p+"v1/accounts/{id}/statuses", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []map[string]any{}
		for _, st := range s.maStatuses {
			out = append(out, s.maJSON(st))
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET "+p+"v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		min, _ := strconv.ParseInt(r.URL.Query().Get("min_id"), 10, 64)
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []map[string]any{}
		for _, n := range s.maNotes {
			if id, _ := strconv.ParseInt(n.ID, 10, 64); id <= min {
				continue
			}
			out = append(out, map[string]any{
				"id": n.ID, "type": n.Type, "created_at": n.At,
				"account": map[string]string{"acct": n.Acct},
				"status": map[string]any{
					"id": n.StatusID, "uri": n.StatusURI, "url": n.StatusURL, "created_at": n.At,
					"in_reply_to_id": nilIfEmpty(n.ReplyTo), "content": n.Content,
				},
			})
		}
		writeJSON(w, 200, out)
	})
	mux.HandleFunc("GET /mastodon/@opencircuitsf/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		var found *maStatus
		for _, st := range s.maStatuses {
			if st.ID == r.PathValue("id") {
				found = st
			}
		}
		s.mu.Unlock()
		s.postPage(w, "Mastodon", found != nil, func() string { return found.Text })
	})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) maJSON(st *maStatus) map[string]any {
	return map[string]any{
		"id": st.ID, "url": st.URL, "uri": st.URI, "created_at": st.CreatedAt,
		"content": "<p>" + html.EscapeString(st.Text) + "</p>", "card": nil,
		"favourites_count": st.Favs, "reblogs_count": st.Reblogs, "replies_count": st.Replies,
	}
}

// -------------------------------------------------------------- ShortLinks

type slCampaign struct {
	ID                                      int64
	Slug, Description, UTMSource, UTMMedium string
}

type slLink struct {
	ID                                                                int64
	Key, Dest, Title, Campaign                                        string
	UTMSource, UTMMedium, UTMCampaign, UTMTerm, UTMContent, Placement string
	Hits                                                              int64 // real redirects served by this mock
}

// baseClicks is a stable, plausible click count for a link.
func baseClicks(key string) int64 { return 4 + int64(hash("clicks", key)%40) }

func (l *slLink) clicks() int64 { return baseClicks(l.Key) + l.Hits }

func (l *slLink) json(active bool) map[string]any {
	return map[string]any{
		"id": l.ID, "key": l.Key, "destination_url": l.Dest, "title": l.Title, "active": active,
		"click_count": l.clicks(), "utm_source": l.UTMSource, "utm_medium": l.UTMMedium,
		"utm_campaign": l.UTMCampaign, "utm_term": l.UTMTerm, "utm_content": l.UTMContent, "placement": l.Placement,
	}
}

func (s *Server) routeShortLinks(mux *http.ServeMux) {
	p := "/shortlinks/api/"
	authed := func(w http.ResponseWriter, r *http.Request) bool {
		if t := bearer(r); t == "" || t == BadToken {
			writeJSON(w, 401, map[string]string{"error": "invalid or missing API token"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET "+p+"me", func(w http.ResponseWriter, r *http.Request) {
		if authed(w, r) {
			writeJSON(w, 200, map[string]any{"id": 1, "email": "demo@opencircuitsf.test", "is_admin": true,
				"base_url": s.public + "/shortlinks/s", "api_token": map[string]any{"id": 1, "name": "demo", "read_only": false}})
		}
	})
	campaignJSON := func(c *slCampaign) map[string]any {
		var n, clicks int64
		for _, l := range s.slLinks {
			if l.Campaign == c.Slug {
				n++
				clicks += l.clicks()
			}
		}
		return map[string]any{"id": c.ID, "slug": c.Slug, "name": c.Slug, "description": c.Description,
			"default_utm_source": c.UTMSource, "default_utm_medium": c.UTMMedium, "archived": false,
			"starts_at": nil, "ends_at": nil, "link_count": n, "total_clicks": clicks}
	}
	mux.HandleFunc("GET "+p+"campaigns/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if c := s.slCampaigns[r.PathValue("slug")]; c != nil {
			writeJSON(w, 200, campaignJSON(c))
			return
		}
		writeJSON(w, 404, map[string]string{"error": "campaign not found"})
	})
	mux.HandleFunc("POST "+p+"campaigns", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var in struct {
			Name             string `json:"name"`
			Description      string `json:"description"`
			DefaultUTMSource string `json:"default_utm_source"`
			DefaultUTMMedium string `json:"default_utm_medium"`
		}
		_ = json.Unmarshal(readBody(r), &in)
		if strings.TrimSpace(in.Name) == "" {
			writeJSON(w, 400, map[string]string{"error": "name is required"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.slCampaigns[in.Name] != nil {
			writeJSON(w, 409, map[string]string{"error": "campaign already exists"})
			return
		}
		s.slSeq++
		c := &slCampaign{ID: s.slSeq, Slug: in.Name, Description: in.Description, UTMSource: in.DefaultUTMSource, UTMMedium: in.DefaultUTMMedium}
		s.slCampaigns[c.Slug] = c
		writeJSON(w, 201, campaignJSON(c))
	})
	mux.HandleFunc("POST "+p+"links", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		var in struct {
			DestinationURL string `json:"destination_url"`
			Title          string `json:"title"`
			Key            string `json:"key"`
			CampaignSlug   string `json:"campaign_slug"`
			UTMSource      string `json:"utm_source"`
			UTMMedium      string `json:"utm_medium"`
			UTMCampaign    string `json:"utm_campaign"`
			UTMTerm        string `json:"utm_term"`
			UTMContent     string `json:"utm_content"`
			Placement      string `json:"placement"`
		}
		_ = json.Unmarshal(readBody(r), &in)
		if u, err := url.Parse(in.DestinationURL); err != nil || u.Host == "" {
			writeJSON(w, 400, map[string]string{"error": "destination_url must be an absolute URL"})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if in.Key != "" {
			if s.slLinks[in.Key] != nil {
				writeJSON(w, 409, map[string]string{"error": "key already in use"})
				return
			}
		} else {
			for _, k := range s.slOrder { // de-duplication when no custom key is given
				l := s.slLinks[k]
				if l.Dest == in.DestinationURL && l.Campaign == in.CampaignSlug && l.UTMContent == in.UTMContent && l.Placement == in.Placement && l.UTMSource == in.UTMSource {
					j := l.json(true)
					j["duplicate"] = true
					writeJSON(w, 200, j)
					return
				}
			}
		}
		s.slSeq++
		key := in.Key
		for key == "" || s.slLinks[key] != nil {
			key = strconv.FormatUint(hash("key", in.DestinationURL, strconv.FormatInt(s.slSeq, 10))%2176782336, 36)
			for len(key) < 6 {
				key = "0" + key
			}
			s.slSeq++
		}
		l := &slLink{ID: s.slSeq, Key: key, Dest: in.DestinationURL, Title: in.Title, Campaign: in.CampaignSlug,
			UTMSource: in.UTMSource, UTMMedium: in.UTMMedium, UTMCampaign: in.UTMCampaign, UTMTerm: in.UTMTerm,
			UTMContent: in.UTMContent, Placement: in.Placement}
		s.slLinks[key] = l
		s.slOrder = append(s.slOrder, key)
		j := l.json(true)
		j["duplicate"] = false
		writeJSON(w, 201, j)
	})
	mux.HandleFunc("GET "+p+"links/{key}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if l := s.slLinks[r.PathValue("key")]; l != nil {
			writeJSON(w, 200, l.json(true))
			return
		}
		writeJSON(w, 404, map[string]string{"error": "link not found"})
	})
	mux.HandleFunc("GET "+p+"links/{key}/qr.svg", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		key := r.PathValue("key")
		s.mu.Lock()
		l := s.slLinks[key]
		s.mu.Unlock()
		if l == nil {
			writeJSON(w, 404, map[string]string{"error": "link not found"})
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, mockQR(key))
	})
	mux.HandleFunc("GET "+p+"campaigns/{slug}/stats", func(w http.ResponseWriter, r *http.Request) {
		if !authed(w, r) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		slug := r.PathValue("slug")
		if s.slCampaigns[slug] == nil {
			writeJSON(w, 404, map[string]string{"error": "campaign not found"})
			return
		}
		writeJSON(w, 200, s.rollup(slug, r.URL.Query().Get("from"), r.URL.Query().Get("to")))
	})
	// Redirects live at base_url + "/u/{key}", as in ShortLinks itself, so a
	// URL built by shortlinks.ShortURL resolves here too (#0603).
	mux.HandleFunc("GET /shortlinks/s/u/{key}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		l := s.slLinks[r.PathValue("key")]
		if l != nil {
			l.Hits++
		}
		s.mu.Unlock()
		if l == nil {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, l.Dest, http.StatusFound)
	})
}

// rollup builds a campaign stats response over the last seven days. Each
// link's total is spread over the days by a stable weighting that sums
// exactly to the total, so every view agrees with every other.
//
// The window is the requested half-open [from, to) of UTC dates (#0593), at
// most 120 days; with no valid request it is the last seven days.
func (s *Server) rollup(slug, from, to string) map[string]any {
	type bucket = map[string]any
	const layout = "2006-01-02"
	var days []string
	if f, ferr := time.Parse(layout, from); ferr == nil {
		if t, terr := time.Parse(layout, to); terr == nil && t.After(f) {
			for d := f; d.Before(t) && len(days) < 120; d = d.AddDate(0, 0, 1) {
				days = append(days, d.Format(layout))
			}
		}
	}
	if len(days) == 0 {
		end := s.now().UTC()
		for i := 0; i < 7; i++ {
			days = append(days, end.AddDate(0, 0, i-6).Format(layout))
		}
	}
	last, _ := time.Parse(layout, days[len(days)-1])
	nd := len(days)
	var total int64
	dayTotals := make([]int64, nd)
	src, med, cont := map[string]int64{}, map[string]int64{}, map[string]int64{}
	byLink, series := []bucket{}, []bucket{}
	keys := append([]string(nil), s.slOrder...)
	sort.Strings(keys)
	for _, k := range keys {
		l := s.slLinks[k]
		if l.Campaign != slug {
			continue
		}
		n := l.clicks()
		total += n
		src[l.UTMSource] += n
		med[l.UTMMedium] += n
		cont[l.UTMContent] += n
		id := l.ID
		byLink = append(byLink, bucket{"link_id": id, "key": l.Key, "title": l.Title, "count": n})
		w := make([]int64, nd)
		var sum int64
		for i := range w {
			w[i] = 1 + int64(hash("day", l.Key, strconv.Itoa(i))%5)
			sum += w[i]
		}
		rem := n
		d := []bucket{}
		for i := range w {
			c := n * w[i] / sum
			if i == nd-1 {
				c = rem
			}
			rem -= c
			dayTotals[i] += c
			if c > 0 {
				d = append(d, bucket{"date": days[i], "count": c})
			}
		}
		series = append(series, bucket{"link_id": id, "key": l.Key, "title": l.Title, "is_other": false, "days": d})
	}
	sort.SliceStable(byLink, func(i, j int) bool { return byLink[i]["count"].(int64) > byLink[j]["count"].(int64) })
	toBuckets := func(m map[string]int64) []bucket {
		out := []bucket{}
		for v, c := range m {
			out = append(out, bucket{"value": v, "count": c})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i]["count"].(int64) != out[j]["count"].(int64) {
				return out[i]["count"].(int64) > out[j]["count"].(int64)
			}
			return out[i]["value"].(string) < out[j]["value"].(string)
		})
		return out
	}
	ts := []bucket{}
	for i, c := range dayTotals {
		if c > 0 {
			ts = append(ts, bucket{"date": days[i], "count": c})
		}
	}
	return map[string]any{
		"click_count": total, "excluded_bot_count": total / 10,
		"by_source": toBuckets(src), "by_medium": toBuckets(med), "by_content": toBuckets(cont),
		"by_referer": []bucket{{"value": "(direct)", "count": total * 6 / 10}, {"value": "bsky.app", "count": total * 3 / 10}, {"value": "mastodon.social", "count": total - total*6/10 - total*3/10}},
		// The window is half-open, [window_from, window_to), as ShortLinks'
		// own is: window_to is the day after the last bucket. Reporting the
		// last day itself made the SPA drop it from the chart, so the legend
		// read 40 against the table's 55 (#0590).
		"window_from": days[0], "window_to": last.AddDate(0, 0, 1).Format(layout),
		"timeseries": bucket{"days": ts}, "by_link": byLink, "series_by_link": series,
	}
}

// mockQR returns a deterministic 25x25 pseudo QR code in the shape
// ShortLinks renders: an XML declaration, a white background rect and a group
// of row-run rects. It is a look-alike for screenshots, not a scannable code.
func mockQR(key string) string {
	const n = 25
	var grid [n][n]bool
	finder := func(ox, oy int) {
		for y := 0; y < 7; y++ {
			for x := 0; x < 7; x++ {
				edge := x == 0 || y == 0 || x == 6 || y == 6
				core := x >= 2 && x <= 4 && y >= 2 && y <= 4
				grid[oy+y][ox+x] = edge || core
			}
		}
	}
	h := hash("qr", key)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			h = h*6364136223846793005 + 1442695040888963407
			grid[y][x] = h>>33&1 == 1
		}
	}
	for y := 0; y < 8; y++ { // clear finder margins
		for x := 0; x < 8; x++ {
			grid[y][x], grid[y][n-1-x], grid[n-1-y][x] = false, false, false
		}
	}
	finder(0, 0)
	finder(n-7, 0)
	finder(0, n-7)
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %d %d\" width=\"%d\" height=\"%d\" shape-rendering=\"crispEdges\">\n", n, n, n*8, n*8)
	fmt.Fprintf(&b, "<rect x=\"0\" y=\"0\" width=\"%d\" height=\"%d\" fill=\"#ffffff\"/>\n<g fill=\"#000000\">\n", n, n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; {
			if !grid[y][x] {
				x++
				continue
			}
			e := x
			for e < n && grid[y][e] {
				e++
			}
			fmt.Fprintf(&b, "<rect x=\"%d\" y=\"%d\" width=\"%d\" height=\"1\"/>\n", x, y, e-x)
			x = e
		}
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}
