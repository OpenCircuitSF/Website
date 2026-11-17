package seo

import (
	"bytes"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
)

func testPNG(t *testing.T, w, h int, noise bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if noise {
		rand.New(rand.NewSource(1)).Read(img.Pix)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLoadGuideArt_Rules(t *testing.T) {
	slug := guides.All()[0].Slug
	good := testPNG(t, cardW, cardH, false)
	big := testPNG(t, cardW, cardH, true)
	if len(big) <= guideArtMaxBytes {
		t.Fatalf("noise fixture is %d bytes, not over the limit", len(big))
	}
	const dir = "art"
	cases := []struct {
		name    string
		files   map[string][]byte
		wantErr string
		wantN   int
	}{
		{"empty dir", map[string][]byte{".keep": nil}, "", 0},
		{"valid", map[string][]byte{slug + ".png": good, ".gitkeep": nil}, "", 1},
		{"unknown slug", map[string][]byte{"no-such-guide.png": good}, "no-such-guide.png", 0},
		{"jpg name", map[string][]byte{slug + ".jpg": good}, slug + ".jpg", 0},
		{"wrong size", map[string][]byte{slug + ".png": testPNG(t, 1200, 600, false)}, "1200x600", 0},
		{"not png", map[string][]byte{slug + ".png": []byte("GIF89a not a png")}, "not a PNG", 0},
		{"too big", map[string][]byte{slug + ".png": big}, "over the", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for n, d := range tc.files {
				fsys[dir+"/"+n] = &fstest.MapFile{Data: d}
			}
			m, err := loadGuideArt(fsys, dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(m) != tc.wantN {
				t.Fatalf("loaded %d, want %d", len(m), tc.wantN)
			}
			if tc.wantN == 1 && !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(m[slug].version) {
				t.Errorf("version = %q, want 8 hex digits", m[slug].version)
			}
		})
	}
}

// TestGuideArt_EmbeddedSetIsValid: the committed images all pass the loader's
// rules. Trivially green with none; real work once art lands.
func TestGuideArt_EmbeddedSetIsValid(t *testing.T) {
	if _, err := loadGuideArt(guideArtFS, "cardassets/guides"); err != nil {
		t.Fatal(err)
	}
}

func TestGuideArtMaxBytesMatchesBluesky(t *testing.T) {
	if guideArtMaxBytes != bluesky.MaxThumbBytes {
		t.Fatalf("guideArtMaxBytes = %d, bluesky.MaxThumbBytes = %d", guideArtMaxBytes, bluesky.MaxThumbBytes)
	}
}

func TestGuideArt_ServedAndInMeta(t *testing.T) {
	all := guides.All()
	a, b := all[0].Slug, all[1].Slug
	pathA, pathB := all[0].Path(), all[1].Path()
	data := testPNG(t, cardW, cardH, false)
	art, err := loadGuideArt(fstest.MapFS{"d/" + a + ".png": {Data: data}}, "d")
	if err != nil {
		t.Fatal(err)
	}
	ver := art[a].version

	r := newRenderer([]byte(testTemplate), testBaseURL, nil, nil, art)
	if got, want := extractMetaContent(t, string(r.Render(pathA)), "og:image"), testBaseURL+"/guides/"+a+"/og.png?v="+ver; got != want {
		t.Errorf("A og:image = %q, want %q", got, want)
	}
	if got, want := extractMetaContent(t, string(r.Render(pathB)), "og:image"), testBaseURL+"/guides/"+b+"/og.png"; got != want {
		t.Errorf("B og:image = %q, want %q", got, want)
	}

	site := newSite([]byte(testTemplate), testBaseURL, nil, nil, art)
	mux := http.NewServeMux()
	mux.Handle("GET /guides/{slug}/og.png", site.GuideCardHandler())
	get := func(path, inm string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	recA := get("/guides/"+a+"/og.png?v="+ver, "")
	if recA.Code != http.StatusOK || !bytes.Equal(recA.Body.Bytes(), data) {
		t.Errorf("A: status %d, art bytes served = %v", recA.Code, bytes.Equal(recA.Body.Bytes(), data))
	}
	if recA.Header().Get("ETag") != art[a].etag || recA.Header().Get("Content-Type") != "image/png" {
		t.Errorf("A headers: %v", recA.Header())
	}
	if rec := get("/guides/"+a+"/og.png", art[a].etag); rec.Code != http.StatusNotModified {
		t.Errorf("A If-None-Match: status %d, want 304", rec.Code)
	}
	recB := get("/guides/"+b+"/og.png", "")
	if recB.Code != http.StatusOK || !strings.HasPrefix(recB.Body.String(), "\x89PNG") || bytes.Equal(recB.Body.Bytes(), data) {
		t.Errorf("B: status %d, want the generated card", recB.Code)
	}
	got, mime, err := site.GuideCard(a)
	if err != nil || mime != "image/png" || !bytes.Equal(got, data) {
		t.Errorf("GuideCard(A) = %d bytes, %q, %v; want the art", len(got), mime, err)
	}
}

var promptHeading = regexp.MustCompile(`(?m)^### ([a-z0-9-]+)$`)

// promptCoverageProblems lists what PROMPTS.md text is missing or has stale
// relative to the guide catalog.
func promptCoverageProblems(text string) []string {
	var problems []string
	sections := map[string]string{}
	locs := promptHeading.FindAllStringSubmatchIndex(text, -1)
	for i, l := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		sections[text[l[2]:l[3]]] = text[l[1]:end]
	}
	catalog := map[string]bool{}
	for _, g := range guides.All() {
		catalog[g.Slug] = true
		sec, ok := sections[g.Slug]
		if !ok {
			problems = append(problems, "missing prompt for "+g.Slug+": add a `### "+g.Slug+"` section")
			continue
		}
		if !strings.Contains(sec, "internal/seo/cardassets/guides/"+g.Slug+".png") {
			problems = append(problems, g.Slug+": section does not name its output path")
		}
	}
	for s := range sections {
		if !catalog[s] {
			problems = append(problems, "stale prompt "+s+": no such guide")
		}
	}
	return problems
}

// TestGuidePromptsCoverCatalog (#0709): every catalog guide has a prompt in
// assets/og/guides/PROMPTS.md, so a new guide is not forgotten.
func TestGuidePromptsCoverCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../assets/og/guides/PROMPTS.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range promptCoverageProblems(string(raw)) {
		t.Error(p)
	}
	// The guard can fail: renaming one heading must name that slug.
	slug := guides.All()[0].Slug
	mutated := strings.Replace(string(raw), "### "+slug+"\n", "### "+slug+"-renamed\n", 1)
	probs := strings.Join(promptCoverageProblems(mutated), "\n")
	if !strings.Contains(probs, "missing prompt for "+slug) {
		t.Errorf("renamed heading not caught; problems: %q", probs)
	}
}
