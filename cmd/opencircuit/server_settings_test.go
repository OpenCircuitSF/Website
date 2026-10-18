package main

import (
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/serversettings"
)

type fakeValuer map[string]string

func (f fakeValuer) Value(k string) string { return f[k] }

func TestApplyBootSettings(t *testing.T) {
	cfg := &config.Config{EmailFrom: "old", EmailReplyTo: "old", EmailListDomain: "old", SendBatchSize: 50}
	applyBootSettings(cfg, fakeValuer{
		serversettings.KeyEmailFrom:       "from@example.org",
		serversettings.KeyEmailReplyTo:    "reply@example.org",
		serversettings.KeyEmailListDomain: "lists.example.org",
		serversettings.KeySendBatchSize:   "25",
	})
	if cfg.EmailFrom != "from@example.org" || cfg.EmailReplyTo != "reply@example.org" || cfg.EmailListDomain != "lists.example.org" || cfg.SendBatchSize != 25 {
		t.Errorf("cfg = %+v", cfg)
	}
	applyBootSettings(cfg, fakeValuer{serversettings.KeySendBatchSize: "lots"})
	if cfg.SendBatchSize != 25 {
		t.Errorf("a non-numeric batch size must keep the old value, got %d", cfg.SendBatchSize)
	}
}

func TestServerSettingsDefaults(t *testing.T) {
	loop := serverSettingsDefaults(&config.Config{BaseURL: "http://localhost:8080"})
	if v, ok := loop[serversettings.KeyMediaDir]; !ok || v != "" {
		t.Errorf("loopback must blank the media default, got %v", loop)
	}
	if got := serverSettingsDefaults(&config.Config{BaseURL: "https://www.example.org"}); got != nil {
		t.Errorf("non-loopback must keep the static default, got %v", got)
	}
}
