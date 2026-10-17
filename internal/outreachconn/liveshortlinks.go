package outreachconn

import (
	"context"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// LiveShortLinks forwards every call to the Manager's current ShortLinks
// client, so a saved change takes effect without rebuilding the consumer. It
// is safe while no client is configured: Enabled reports false and every
// other method returns shortlinks.ErrUnavailable.
type LiveShortLinks struct{ m *Manager }

// ShortLinker returns the live adapter.
func (m *Manager) ShortLinker() *LiveShortLinks { return &LiveShortLinks{m: m} }

func (l *LiveShortLinks) Enabled() bool { return l.m.ShortLinks().Enabled() }

func (l *LiveShortLinks) Me(ctx context.Context) (*shortlinks.Me, error) {
	return l.m.ShortLinks().Me(ctx)
}

func (l *LiveShortLinks) EnsureCampaign(ctx context.Context, slug string, req shortlinks.CreateCampaignRequest) (*shortlinks.Campaign, error) {
	return l.m.ShortLinks().EnsureCampaign(ctx, slug, req)
}

func (l *LiveShortLinks) CreateLink(ctx context.Context, req shortlinks.CreateLinkRequest) (*shortlinks.Link, error) {
	return l.m.ShortLinks().CreateLink(ctx, req)
}

func (l *LiveShortLinks) LinkQRSVG(ctx context.Context, key string) ([]byte, error) {
	return l.m.ShortLinks().LinkQRSVG(ctx, key)
}

func (l *LiveShortLinks) CampaignStats(ctx context.Context, slug string, from, to time.Time) (*shortlinks.CampaignRollup, error) {
	return l.m.ShortLinks().CampaignStats(ctx, slug, from, to)
}
