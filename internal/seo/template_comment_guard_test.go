// #0534: web/index.html — the source template Renderer substitutes into and
// every real `npm run build` copies into web/dist/index.html — must carry no
// HTML comments. Its explanatory comments used to ship in the bytes every
// visitor receives: internal issue numbers and PRD references, published to
// anyone viewing source, on every page. They have also fooled tooling here
// twice (a regex hunting a tag found the one inside a comment when #0064's
// CSP hash was computed; a naive heading count found two headings on every
// page during #0519's review), so the template is now kept comment-free and
// the knowledge lives where developers read it — docs/seo.md's
// "## Template conventions" section and the doc comments beside the token
// constants in seo.go.
//
// The oracle is a containment scan for the opener byte sequence, not a
// regex over markup: per the HTML standard every comment, including the
// abruptly-closed `<!-->` and `<!--->` forms, begins with that exact
// four-byte sequence, so finding none proves there are none. It is
// non-circular by the #0304 rule — the subject is a file this test does not
// own, so re-adding a comment to web/index.html changes what the scan
// observes and fails the test directly.
//
// Two deliberate exceptions, neither in this file's scope:
//
//   - web/dist/index.html's "MINIMAL PLACEHOLDER" marker comment. It is
//     pinned by TestDistIndexPlaceholder (#0141) precisely because a real
//     build does NOT contain it, and the placeholder itself never ships —
//     `npm run build` rewrites the file before anything serves it.
//   - The JavaScript comments inside the bootstrap <script>. They are part
//     of the script's bytes, so stripping them would change the CSP
//     `script-src` hash pinned in deploy/apache/opencircuitsf.com.conf —
//     exactly what #0534's criterion 3 forbids. They are script source,
//     not HTML comments, and never parse as markup.
//
// Filesystem-only — no TEST_DATABASE_URL, no advisory lock — matching
// dist_placeholder_guard_test.go's placement rationale: it rides every
// `go test ./internal/...` run for free and is never skipped.
package seo

import (
	"os"
	"strings"
	"testing"
)

func TestSourceTemplate_CarriesNoHTMLComments(t *testing.T) {
	// Same path convention as TestSourceTemplate_EachTokenAppearsExactlyOnce
	// (seo_test.go): relative to this package's directory, the real source
	// template rather than the embedded placeholder dist, so the check is
	// meaningful from a clean checkout.
	const templatePath = "../../web/index.html"
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("reading %s: %v -- has the repo layout changed?", templatePath, err)
	}
	if n := strings.Count(string(raw), "<!--"); n != 0 {
		t.Errorf("%s carries %d HTML comment(s); comments ship to every visitor and have twice fooled tooling here (#0064's CSP hash, #0519's heading count). Move the explanation to docs/seo.md's Template conventions section or a doc comment beside the token constants in seo.go, and delete the comment (#0534).", templatePath, n)
	}
}
