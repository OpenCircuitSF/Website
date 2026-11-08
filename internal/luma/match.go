package luma

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Workshop is the part of a site event the comparison reads. Address is ""
// when the event has no venue.
type Workshop struct {
	ID      int64
	Title   string
	Start   time.Time
	End     *time.Time
	Address string
}

// Diff is one field that differs between a site event and its Luma copy,
// with both values formatted for display.
type Diff struct {
	Field string `json:"field"`
	Label string `json:"label"`
	Site  string `json:"site"`
	Luma  string `json:"luma"`
}

// Workshop states.
const (
	StateOnLuma    = "on_luma"
	StateDiffers   = "differs"
	StateNotOnLuma = "not_on_luma"
)

// Result is one workshop's comparison. Event is nil for StateNotOnLuma; Diffs
// is never nil.
type Result struct {
	WorkshopID int64
	State      string
	Event      *Event
	Diffs      []Diff
}

// pacific is the zone event dates and times are compared and shown in.
var pacific = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic("luma: load America/Los_Angeles: " + err.Error())
	}
	return loc
}()

// minTitleScore is the title agreement a same-day Luma event needs to be a
// candidate before the one-each fallback.
const minTitleScore = 0.5

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "and": true, "for": true,
	"with": true, "in": true, "on": true, "at": true, "learn": true, "how": true,
}

func titleTokens(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !stopWords[t] {
			out = append(out, t)
		}
	}
	return out
}

func tokensAgree(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 3 && strings.HasPrefix(b, a)
}

// titleScore is the share of the shorter title's words that agree with some
// word of the other (equal, or a prefix of at least 3 letters), stop words
// left out. It is 0 when either title has no words left.
func titleScore(a, b string) float64 {
	ta, tb := titleTokens(a), titleTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	if len(ta) > len(tb) {
		ta, tb = tb, ta
	}
	n := 0
	for _, x := range ta {
		for _, y := range tb {
			if tokensAgree(x, y) {
				n++
				break
			}
		}
	}
	return float64(n) / float64(len(ta))
}

var streetStart = regexp.MustCompile(`^\d+[A-Za-z]?\s+\S`)

var streetWords = map[string]string{
	"street": "st", "avenue": "ave", "boulevard": "blvd", "road": "rd", "drive": "dr",
	"place": "pl", "lane": "ln", "court": "ct", "terrace": "ter", "highway": "hwy",
	"north": "n", "south": "s", "east": "e", "west": "w", "suite": "ste",
}

// streetTypes are the normalised street-type words that end a street line.
var streetTypes = map[string]bool{
	"st": true, "ave": true, "blvd": true, "rd": true, "dr": true, "pl": true, "ln": true,
	"ct": true, "ter": true, "hwy": true, "way": true, "pkwy": true, "sq": true,
}

// streetPart is the normalised street line of an address ("1736 18th st"),
// or "" when no comma-separated segment starts with a house number. The line
// is cut after the first street-type word at index 2 or later, so an address
// written without a comma after the street ("1736 18th St San Francisco")
// gives the same line as one with it. Index 0 and 1 are skipped so a street
// named after a saint ("100 St Francis Blvd") keeps its name.
func streetPart(addr string) string {
	for _, seg := range strings.Split(addr, ",") {
		seg = strings.TrimSpace(seg)
		if !streetStart.MatchString(seg) {
			continue
		}
		seg = strings.ToLower(seg)
		seg = strings.NewReplacer(".", "", "#", "").Replace(seg)
		words := strings.Fields(seg)
		for i, w := range words {
			if s, ok := streetWords[w]; ok {
				words[i] = s
			}
		}
		for i := 2; i < len(words); i++ {
			if streetTypes[words[i]] {
				words = words[:i+1]
				break
			}
		}
		return strings.Join(words, " ")
	}
	return ""
}

func dateKey(t time.Time) string { return t.In(pacific).Format("2006-01-02") }

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// Compare pairs each workshop with at most one Luma event and reports what
// differs. A Luma event is a candidate for a workshop when both start on the
// same Pacific date and their titles score at least minTitleScore; candidate
// pairs are taken best first (score, then closest start). Afterwards, a date
// left with exactly one unmatched workshop and one unmatched Luma event pairs
// them whatever the titles say. Results follow the order of ws; leftovers are
// the unmatched Luma events starting at or after now, in start order.
func Compare(ws []Workshop, evs []Event, now time.Time) ([]Result, []Event) {
	type pair struct {
		w, e  int
		score float64
		gap   time.Duration
	}
	var pairs []pair
	for i, w := range ws {
		for j, e := range evs {
			if dateKey(w.Start) != dateKey(e.Start) {
				continue
			}
			if s := titleScore(w.Title, e.Summary); s >= minTitleScore {
				pairs = append(pairs, pair{i, j, s, abs(w.Start.Sub(e.Start))})
			}
		}
	}
	sort.SliceStable(pairs, func(a, b int) bool {
		pa, pb := pairs[a], pairs[b]
		if pa.score != pb.score {
			return pa.score > pb.score
		}
		if pa.gap != pb.gap {
			return pa.gap < pb.gap
		}
		if ws[pa.w].ID != ws[pb.w].ID {
			return ws[pa.w].ID < ws[pb.w].ID
		}
		return evs[pa.e].UID < evs[pb.e].UID
	})
	wMatch := make([]int, len(ws))
	for i := range wMatch {
		wMatch[i] = -1
	}
	eUsed := make([]bool, len(evs))
	for _, p := range pairs {
		if wMatch[p.w] >= 0 || eUsed[p.e] {
			continue
		}
		wMatch[p.w] = p.e
		eUsed[p.e] = true
	}
	// Fallback: one unmatched of each on the same date.
	wByDate, eByDate := map[string][]int{}, map[string][]int{}
	for i, w := range ws {
		if wMatch[i] < 0 {
			wByDate[dateKey(w.Start)] = append(wByDate[dateKey(w.Start)], i)
		}
	}
	for j, e := range evs {
		if !eUsed[j] {
			eByDate[dateKey(e.Start)] = append(eByDate[dateKey(e.Start)], j)
		}
	}
	for d, wi := range wByDate {
		if ej := eByDate[d]; len(wi) == 1 && len(ej) == 1 {
			wMatch[wi[0]] = ej[0]
			eUsed[ej[0]] = true
		}
	}

	results := make([]Result, len(ws))
	for i, w := range ws {
		r := Result{WorkshopID: w.ID, State: StateNotOnLuma, Diffs: []Diff{}}
		if j := wMatch[i]; j >= 0 {
			e := evs[j]
			r.Event = &e
			r.Diffs = diffs(w, e)
			r.State = StateOnLuma
			if len(r.Diffs) > 0 {
				r.State = StateDiffers
			}
		}
		results[i] = r
	}
	var left []Event
	for j, e := range evs {
		if !eUsed[j] && !e.Start.Before(now) {
			left = append(left, e)
		}
	}
	sort.SliceStable(left, func(a, b int) bool { return left[a].Start.Before(left[b].Start) })
	return results, left
}

func diffs(w Workshop, e Event) []Diff {
	out := []Diff{}
	if d, ok := timeDiff("start", "Start", w.Start, e.Start); ok {
		out = append(out, d)
	}
	if w.End != nil && e.End != nil {
		if d, ok := timeDiff("end", "End", *w.End, *e.End); ok {
			out = append(out, d)
		}
	}
	if a, b := streetPart(w.Address), streetPart(e.Location); a != "" && b != "" && a != b {
		out = append(out, Diff{Field: "location", Label: "Location", Site: w.Address, Luma: e.Location})
	}
	return out
}

func timeDiff(field, label string, site, lu time.Time) (Diff, bool) {
	s, l := site.Truncate(time.Minute), lu.Truncate(time.Minute)
	if s.Equal(l) {
		return Diff{}, false
	}
	layout := "3:04 PM"
	if dateKey(s) != dateKey(l) {
		layout = "Jan 2, 3:04 PM"
	}
	return Diff{Field: field, Label: label, Site: s.In(pacific).Format(layout), Luma: l.In(pacific).Format(layout)}, true
}
