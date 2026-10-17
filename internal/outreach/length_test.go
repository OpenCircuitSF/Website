package outreach

import (
	"strings"
	"testing"
)

func TestLength(t *testing.T) {
	bsky, _ := ChannelByName("bluesky")
	masto, _ := ChannelByName("mastodon")
	zwj := "\U0001F469‍\U0001F469‍\U0001F467"
	if got := Length(bsky, "hi "+zwj); got != 4 {
		t.Errorf("bluesky ZWJ family counts once: got %d", got)
	}
	long := "https://example.test/" + strings.Repeat("a", 80)
	if got := Length(masto, "see "+long); got != 4+23 {
		t.Errorf("mastodon URL weight: got %d", got)
	}
	if got := Length(bsky, "see "+long); got != 4+len(long) {
		t.Errorf("bluesky counts a URL in full: got %d", got)
	}
}

func TestValidateForSend(t *testing.T) {
	bsky, _ := ChannelByName("bluesky")
	masto, _ := ChannelByName("mastodon")
	insta, _ := ChannelByName("instagram")
	if err := ValidateForSend(bsky, strings.Repeat("x", 300)); err != nil {
		t.Errorf("300 should pass: %v", err)
	}
	err := ValidateForSend(bsky, strings.Repeat("x", 301))
	if err == nil || !strings.Contains(err.Error(), "301") || !strings.Contains(err.Error(), "300") {
		t.Errorf("301 should fail naming count and limit: %v", err)
	}
	if err := ValidateForSend(masto, strings.Repeat("x", 500)); err != nil {
		t.Errorf("mastodon 500 should pass: %v", err)
	}
	if err := ValidateForSend(masto, strings.Repeat("x", 501)); err == nil {
		t.Error("mastodon 501 should fail")
	}
	if err := ValidateForSend(bsky, "  "); err == nil {
		t.Error("blank should fail")
	}
	if err := ValidateForSend(bsky, "go {link}"); err == nil {
		t.Error("an unreplaced link token should fail")
	}
	if err := ValidateForSend(insta, strings.Repeat("x", 5000)); err != nil {
		t.Errorf("a channel with no limit should not enforce one: %v", err)
	}
}
