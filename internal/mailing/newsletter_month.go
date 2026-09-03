// NewsletterMonth (#0405, PRD §6.8): the opt-in "this campaign is a monthly
// newsletter" template CampaignInput.NewsletterMonth carries into Create.
// See campaigns.go's CampaignInput doc comment and Create's own comment for
// how the value is consumed — this file owns only parsing and formatting.
//
// # Two formats, deliberately inverted, and why that is safe
//
// The wire format ParseNewsletterMonth accepts is "YYYY-MM" — what HTML's
// <input type="month"> produces. The slug format Slug returns is "MM-YYYY"
// — the user's own decision (issues/0405.md: "It can be short and
// specific"). Because parseNewsletterMonthPattern anchors four digits
// first, a value in the MM-YYYY slug shape fed back in as if it were the
// YYYY-MM wire shape cannot parse as anything at all — it is rejected
// outright by ParseNewsletterMonth, never silently misread as some other
// month. The two formats can therefore never be confused with each other in
// a way that produces a wrong-but-valid result. (This repository's own
// no-literal-slug-value convention for #0405 — see issues/0405.md §6 — is
// why this comment describes the property rather than quoting an example
// slug.)
//
// # Why this decision does not retroactively change minted slugs
//
// newsletterSlugFormat (campaigns.go) is a constant, not a settings row —
// issues/0405.md §4 records the reasoning. Changing it here in a future
// commit does not, and must not, alter a slug already written to
// email_campaigns.slug; Create is the only writer and it runs once, at
// INSERT time.
package mailing

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// ErrInvalidNewsletterMonth is returned by ParseNewsletterMonth for a
// malformed or out-of-range input, and by CampaignStore.Create when a
// caller supplies a NewsletterMonth that fails its own valid() check
// (belt-and-braces behind ParseNewsletterMonth — mirrors
// normalizeAudience's defence-in-depth shape against ErrUnknownAudienceMode).
var ErrInvalidNewsletterMonth = errors.New("mailing: newsletter_month must be YYYY-MM")

// parseNewsletterMonthPattern anchors four digits first, so a transposed
// "MM-YYYY" value can never parse — see this file's doc comment.
var parseNewsletterMonthPattern = regexp.MustCompile(`^(\d{4})-(0[1-9]|1[0-2])$`)

// NewsletterMonth is a validated (year, month) pair opting a campaign into
// #0405's MM-YYYY archive-slug template. Fields are unexported so the only
// way to construct a valid value outside this package is
// ParseNewsletterMonth — the range rule lives in exactly one place.
type NewsletterMonth struct {
	year  int
	month time.Month
}

// ParseNewsletterMonth parses s as "YYYY-MM" (HTML <input type="month">'s
// own value format). A zero-padded month is required; "2026-9" is rejected
// alongside genuinely malformed input, and so is any value in the
// transposed MM-YYYY slug shape, because the pattern anchors four digits
// first.
func ParseNewsletterMonth(s string) (NewsletterMonth, error) {
	m := parseNewsletterMonthPattern.FindStringSubmatch(s)
	if m == nil {
		return NewsletterMonth{}, ErrInvalidNewsletterMonth
	}
	year, err := strconv.Atoi(m[1])
	if err != nil {
		return NewsletterMonth{}, ErrInvalidNewsletterMonth
	}
	monthNum, err := strconv.Atoi(m[2])
	if err != nil {
		return NewsletterMonth{}, ErrInvalidNewsletterMonth
	}
	nm := NewsletterMonth{year: year, month: time.Month(monthNum)}
	if !nm.valid() {
		return NewsletterMonth{}, ErrInvalidNewsletterMonth
	}
	return nm, nil
}

// valid reports whether m carries an in-range year and month. A bare
// NewsletterMonth{} (month 0) is invalid — the zero value must never be
// mistaken for a real one. This is the same check ParseNewsletterMonth
// applies to its own result and Create applies to a caller-constructed
// value; see this file's package doc comment.
func (m NewsletterMonth) valid() bool {
	return m.year >= 1 && m.month >= time.January && m.month <= time.December
}

// Slug returns the MM-YYYY archive-slug template value — issues/0405.md's
// decision, zero-padded two-digit month, hyphen, four-digit year.
func (m NewsletterMonth) Slug() string {
	return fmt.Sprintf("%02d-%04d", int(m.month), m.year)
}
