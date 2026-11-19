package seo

import (
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestHeadAdvertisesEventsCalendar is #0732: both the source and the tracked
// placeholder head carry exactly one alternate link to the /events.ics feed.
// Tokenized rather than regex-matched, so a tag inside a comment never counts.
func TestHeadAdvertisesEventsCalendar(t *testing.T) {
	for _, p := range []string{"../../web/index.html", "../../web/dist/index.html"} {
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("open %s: %v", p, err)
		}
		z := html.NewTokenizer(f)
		n := 0
		for {
			tt := z.Next()
			if tt == html.ErrorToken {
				if z.Err() != io.EOF {
					t.Fatalf("%s: tokenize: %v", p, z.Err())
				}
				break
			}
			if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
				continue
			}
			tok := z.Token()
			if tok.Data != "link" {
				continue
			}
			a := map[string]string{}
			for _, at := range tok.Attr {
				a[at.Key] = at.Val
			}
			if strings.EqualFold(a["rel"], "alternate") && a["type"] == "text/calendar" && a["href"] == "/events.ics" {
				n++
			}
		}
		f.Close()
		if n != 1 {
			t.Errorf("%s: %d events.ics alternate links, want exactly 1", p, n)
		}
	}
}
