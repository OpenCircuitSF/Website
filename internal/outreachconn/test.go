package outreachconn

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

const testTimeout = 20 * time.Second

// Test checks one group's saved, effective credentials against its service
// with a harmless read. It always builds a fresh client, so a cached Bluesky
// session cannot hide a wrong password. The result never carries upstream
// text or a secret.
func (m *Manager) Test(ctx context.Context, group string) TestResult {
	v := m.cur.Load().values
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	switch group {
	case GroupShortLinks:
		c, err := shortlinks.New(v[KeyShortLinksBaseURL], v[KeyShortLinksAPIToken])
		if err != nil {
			return TestResult{Error: errShortLinks}
		}
		if c == nil {
			return TestResult{Error: "Not configured: set the base URL and API token first."}
		}
		me, err := c.Me(ctx)
		if err != nil {
			m.log.Warn("outreach: connection test failed", "group", group, "err", err)
			return TestResult{Error: shortLinksTestError(err)}
		}
		return TestResult{OK: true, Detail: "Connected to ShortLinks as " + me.Email}
	case GroupBluesky:
		c, err := bluesky.New(v[KeyBlueskyPDSURL], v[KeyBlueskyHandle], v[KeyBlueskyAppPassword])
		if err != nil {
			return TestResult{Error: errBluesky}
		}
		if c == nil {
			return TestResult{Error: "Not configured: set the handle and app password first."}
		}
		handle, err := c.VerifyCredentials(ctx)
		if err != nil {
			m.log.Warn("outreach: connection test failed", "group", group, "err", err)
			return TestResult{Error: blueskyTestError(err)}
		}
		return TestResult{OK: true, Detail: "Signed in to Bluesky as @" + handle}
	case GroupMastodon:
		c, err := mastodon.New(v[KeyMastodonBaseURL], v[KeyMastodonToken])
		if err != nil {
			return TestResult{Error: errMastodon}
		}
		if c == nil {
			return TestResult{Error: "Not configured: set the instance URL and access token first."}
		}
		acct, err := c.VerifyCredentials(ctx)
		if err != nil {
			m.log.Warn("outreach: connection test failed", "group", group, "err", err)
			return TestResult{Error: mastodonTestError(err)}
		}
		if acct.Acct != "" {
			m.rememberMastodonAccount(v, acct.Acct)
		}
		return TestResult{OK: true, Detail: "Signed in to Mastodon as @" + acct.Acct}
	}
	return TestResult{Error: "unknown connection"}
}

func shortLinksTestError(err error) string {
	var ae *shortlinks.APIError
	switch {
	case errors.Is(err, shortlinks.ErrUnauthenticated), errors.Is(err, shortlinks.ErrForbidden):
		return "ShortLinks rejected the API token."
	case errors.Is(err, shortlinks.ErrRateLimited):
		return "ShortLinks is rate limiting requests; try again shortly."
	case errors.As(err, &ae):
		return fmt.Sprintf("ShortLinks answered HTTP %d.", ae.Status)
	}
	return "Could not reach ShortLinks at the saved base URL."
}

func blueskyTestError(err error) string {
	var ae *bluesky.APIError
	switch {
	case errors.Is(err, bluesky.ErrAuth):
		return "Bluesky rejected the handle or app password."
	case errors.Is(err, bluesky.ErrRateLimited):
		return "Bluesky is rate limiting sign-ins; try again in a few minutes."
	case errors.As(err, &ae):
		return fmt.Sprintf("Bluesky answered HTTP %d.", ae.Status)
	}
	return "Could not reach the Bluesky PDS."
}

func mastodonTestError(err error) string {
	var ae *mastodon.APIError
	if errors.As(err, &ae) {
		switch {
		case ae.Status == 401:
			return "Mastodon rejected the access token."
		case ae.Status == 403:
			return "The token is valid but lacks the profile (or read:accounts) scope this check needs."
		case errors.Is(err, mastodon.ErrRateLimited):
			return "Mastodon is rate limiting requests; try again shortly."
		}
		return fmt.Sprintf("Mastodon answered HTTP %d.", ae.Status)
	}
	return "Could not reach the Mastodon instance."
}

// rememberMastodonAccount caches "@user@host" for Account. Mastodon reports a
// local account without its host, so the instance host is appended. A result
// fetched under a config that is no longer current is discarded (#0597): a
// slow lookup for a replaced token must not overwrite the newer one's answer.
func (m *Manager) rememberMastodonAccount(v map[string]string, acct string) {
	acct = strings.TrimPrefix(acct, "@")
	if !strings.Contains(acct, "@") {
		u, err := url.Parse(strings.TrimSpace(v[KeyMastodonBaseURL]))
		if err != nil || u.Host == "" {
			return
		}
		acct += "@" + u.Host
	}
	key := strings.Join([]string{v[KeyMastodonBaseURL], v[KeyMastodonToken]}, "\x00")
	if key != m.cur.Load().maKey {
		return
	}
	m.maAcct.Store(&maAccount{key: key, acct: "@" + acct})
}
