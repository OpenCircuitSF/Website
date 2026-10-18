package devmocks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreachconn"
)

// DemoSlug is the fictional workshop's slug.
const DemoSlug = "led-programming"

// RequireLocalDSN refuses any database URL whose host is not this machine, so
// the demo seed can never write to a shared or production database.
func RequireLocalDSN(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return errors.New("devmocks: DATABASE_URL must be a postgres URL with an explicit local host")
	}
	h := u.Hostname()
	if h == "localhost" {
		return nil
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("devmocks: refusing to seed %q: the demo seed only targets a local database", h)
}

// SeedDemo creates the fictional LED Programming workshop, a handful of
// outreach locations, the cover image under mediaDir, and saves the mock
// service URLs and credentials as outreach connection values. It is
// idempotent. mockURL is the mocks' base URL (no trailing slash).
func SeedDemo(ctx context.Context, pool *pgxpool.Pool, mediaDir, mockURL string, now time.Time) error {
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return fmt.Errorf("devmocks: media dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "led-programming.png"), coverPNG(), 0o644); err != nil {
		return fmt.Errorf("devmocks: write cover: %w", err)
	}
	start := time.Date(now.Year(), now.Month(), now.Day()+14, 18, 30, 0, 0, time.FixedZone("PT", -7*3600)).UTC()
	_, err := pool.Exec(ctx, `
		INSERT INTO workshops (slug, title, summary, body_md, starts_at, ends_at, location_name, location_address,
		                       location_note, capacity, cover_image, status, published_at)
		VALUES ($1, 'LED Programming', $2, $3, $4, $5, 'Mission Makerspace Annex',
		        '2145 Folsom St, San Francisco, CA (fictional demo venue)', 'Enter through the roll-up door on 17th St.',
		        12, '/media/led-programming.png', 'published', $6)
		ON CONFLICT (slug) DO UPDATE SET title = EXCLUDED.title, summary = EXCLUDED.summary, body_md = EXCLUDED.body_md,
		  starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, location_name = EXCLUDED.location_name,
		  location_address = EXCLUDED.location_address, location_note = EXCLUDED.location_note,
		  capacity = EXCLUDED.capacity, cover_image = EXCLUDED.cover_image, status = 'published', updated_at = now()`,
		DemoSlug,
		"Wire up an LED strip and make it dance: a hands-on evening of microcontroller programming for complete beginners.",
		"## Make light do what you tell it\n\nBring a laptop. We provide the boards, the LED strips and the snacks. "+
			"By the end of the evening your strip will chase, pulse and react to a button press.\n\n"+
			"No electronics experience needed.\n",
		start, start.Add(2*time.Hour), now.UTC())
	if err != nil {
		return fmt.Errorf("devmocks: seed workshop: %w", err)
	}
	for _, l := range []struct{ name, code, addr, notes string }{
		{"Mission Branch Library", "mission-lib", "300 Bartlett St, San Francisco", "Community board by the front desk."},
		{"Haight Coffee Co-op", "haight-coop", "1500 Haight St, San Francisco", "Corkboard beside the register."},
		{"SoMa Hardware Store", "soma-hw", "900 Howard St, San Francisco", "Ask the manager before posting."},
		{"Civic Center Makerspace", "civic-mkr", "50 Hyde St, San Francisco", "Lobby bulletin board."},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO outreach_locations (name, code, address, notes) VALUES ($1, $2, $3, $4)
			ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, address = EXCLUDED.address, notes = EXCLUDED.notes`,
			l.name, l.code, l.addr, l.notes); err != nil {
			return fmt.Errorf("devmocks: seed location %s: %w", l.code, err)
		}
	}
	mgr := outreachconn.NewManager(outreachconn.NewStore(pool), nil, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if _, err := mgr.Apply(ctx, ConnectionValues(mockURL), now); err != nil {
		return fmt.Errorf("devmocks: save connection values: %w", err)
	}
	return nil
}

// ConnectionValues are the outreach connection settings that point at the mocks.
func ConnectionValues(mockURL string) map[string]string {
	return map[string]string{
		outreachconn.KeyShortLinksBaseURL:  mockURL + "/shortlinks",
		outreachconn.KeyShortLinksAPIToken: "demo-shortlinks-token",
		outreachconn.KeyBlueskyPDSURL:      mockURL + "/bluesky",
		outreachconn.KeyBlueskyHandle:      "opencircuitsf.demo",
		outreachconn.KeyBlueskyAppPassword: "demo-app-password",
		outreachconn.KeyMastodonBaseURL:    mockURL + "/mastodon",
		outreachconn.KeyMastodonToken:      "demo-mastodon-token",
	}
}

// coverPNG draws a 1200x630 placeholder: a dark field with a row of lit LEDs.
func coverPNG() []byte {
	const w, h = 1200, 630
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{0x0c, 0x10, 0x14, 0xff}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, bg)
		}
	}
	for i := 0; i < 12; i++ {
		cx, cy := 100+i*91, 315
		c := color.RGBA{uint8(40 + (i*17)%200), uint8(255 - i*15), uint8(35 + i*18), 0xff}
		for y := -34; y <= 34; y++ {
			for x := -34; x <= 34; x++ {
				if x*x+y*y <= 34*34 {
					img.Set(cx+x, cy+y, c)
				}
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
