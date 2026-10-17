package outreach

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// FlyerTemplates lists the flyer layouts. The same list lives in the
// outreach_flyers_template_check constraint and in web/src/lib/flyers.ts.
var FlyerTemplates = []string{"photo-top", "full-photo", "split"}

// ValidFlyerTemplate reports whether t names a flyer template.
func ValidFlyerTemplate(t string) bool {
	for _, x := range FlyerTemplates {
		if x == t {
			return true
		}
	}
	return false
}

// Flyer selection bounds.
const (
	MaxFlyerLocations = 40
	MinFlyerCopies    = 1
	MaxFlyerCopies    = 50
	MaxTaglineRunes   = 140
)

var (
	// ErrFlyerNotFound is returned when no flyer matches an id.
	ErrFlyerNotFound = errors.New("outreach: flyer not found")
	// ErrFlyerLinkNotFound is returned when no flyer link matches an id.
	ErrFlyerLinkNotFound = errors.New("outreach: flyer link not found")
	// ErrLocationCodeChanged is returned by InsertFlyerLink when the
	// location's code is no longer the one the link's destination was built
	// from.
	ErrLocationCodeChanged = errors.New("outreach: location code changed")
	// ErrLocationInactive is returned when an inactive location that is not
	// already selected is added to a flyer.
	ErrLocationInactive = errors.New("outreach: location is inactive")
	// ErrFlyerSelectionInvalid is returned for a selection with a bad copy
	// count, a duplicate or an unknown location, or too many entries.
	ErrFlyerSelectionInvalid = errors.New("outreach: invalid flyer selection")
)

// Flyer is one row of outreach_flyers.
type Flyer struct {
	ID            int64
	OutreachID    int64
	Photo         *string
	FocalX        float64
	FocalY        float64
	Template      string
	Tagline       string
	CreatedAt     time.Time
	UpdatedAt     *time.Time
	WorkshopID    int64  // filled by ListFlyers
	WorkshopSlug  string // filled by ListFlyers
	WorkshopTitle string // filled by ListFlyers
}

// FlyerInput is the data for a new flyer.
type FlyerInput struct {
	OutreachID int64
	Photo      *string
	FocalX     float64
	FocalY     float64
	Template   string
	Tagline    string
}

// FlyerPatch is a partial update; a nil field is left unchanged. ClearPhoto
// sets the photo to NULL.
type FlyerPatch struct {
	Photo      *string
	ClearPhoto bool
	FocalX     *float64
	FocalY     *float64
	Template   *string
	Tagline    *string
}

// FlyerLink is one row of outreach_flyer_links without its QR bytes.
type FlyerLink struct {
	ID         int64
	OutreachID int64
	LocationID int64
	Key        string
	ShortURL   string
	HasQR      bool
	PutUpAt    *time.Time
}

// FlyerSelection is one location chosen for a flyer, with the shared link
// when one has been generated.
type FlyerSelection struct {
	LocationID int64
	Name       string
	Code       string
	Address    string
	Active     bool
	Copies     int
	Link       *FlyerLink
}

// SelectionInput is one entry of a flyer's location selection.
type SelectionInput struct {
	LocationID int64
	Copies     int
}

// FlyerLinkAll is a flyer link with the context the results view needs.
type FlyerLinkAll struct {
	FlyerLink
	CampaignSlug      string
	OutreachCreatedAt time.Time
	WorkshopStartsAt  *time.Time
	LocationName      string
	Code              string
}

// FlyerDestinationURL is the workshop URL a flyer link points at. Keys are
// sorted (url.Values.Encode), so the result is deterministic for a given
// (workshop, location code), which ShortLinks' de-duplication on destination
// depends on.
func FlyerDestinationURL(siteBaseURL, slug, code string) string {
	q := url.Values{}
	q.Set("utm_source", "flyer")
	q.Set("utm_medium", "print")
	q.Set("utm_campaign", slug)
	q.Set("utm_content", code)
	return strings.TrimRight(siteBaseURL, "/") + "/workshops/" + slug + "?" + q.Encode()
}

// FlyerDateLine renders a workshop's time for a flyer in Pacific time:
// "Sat, Nov 14 · 10:00 AM–12:30 PM". It is "Date TBA" without a start, and
// has no end part when the end is missing or falls on a different day.
func FlyerDateLine(startsAt, endsAt *time.Time) string {
	if startsAt == nil {
		return "Date TBA"
	}
	s := startsAt.In(pacific)
	out := s.Format("Mon, Jan 2 · 3:04 PM")
	if endsAt != nil {
		e := endsAt.In(pacific)
		sy, sm, sd := s.Date()
		ey, em, ed := e.Date()
		if sy == ey && sm == em && sd == ed {
			out += "–" + e.Format("3:04 PM")
		}
	}
	return out
}

// FlyerPlaceLine joins the workshop's location name and address for a flyer,
// or "Location TBA" when both are empty.
func FlyerPlaceLine(name, address *string) string {
	var parts []string
	for _, p := range []*string{name, address} {
		if p != nil && strings.TrimSpace(*p) != "" {
			parts = append(parts, strings.TrimSpace(*p))
		}
	}
	if len(parts) == 0 {
		return "Location TBA"
	}
	return strings.Join(parts, ", ")
}

// FlyerClicksByKey sums a rollup's per-link clicks by short link key.
func FlyerClicksByKey(links []FlyerLink, r *shortlinks.CampaignRollup) map[string]int64 {
	out := map[string]int64{}
	if r == nil {
		return out
	}
	want := map[string]bool{}
	for _, l := range links {
		if l.Key != "" {
			want[l.Key] = true
			out[l.Key] = 0
		}
	}
	for _, row := range r.ByLink {
		if want[row.Key] {
			out[row.Key] += row.Count
		}
	}
	return out
}

const flyerCols = `f.id, f.outreach_id, f.photo, f.focal_x, f.focal_y, f.template, f.tagline, f.created_at, f.updated_at`

func scanFlyer(row pgx.Row) (Flyer, error) {
	var f Flyer
	err := row.Scan(&f.ID, &f.OutreachID, &f.Photo, &f.FocalX, &f.FocalY, &f.Template, &f.Tagline, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

// ListFlyers returns flyers newest first, with the workshop title. A zero
// outreachID lists every flyer.
func (s *Store) ListFlyers(ctx context.Context, outreachID int64) ([]Flyer, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+flyerCols+`, o.workshop_id, w.slug, w.title
		FROM outreach_flyers f
		JOIN outreach o ON o.id = f.outreach_id
		JOIN workshops w ON w.id = o.workshop_id
		WHERE $1 = 0 OR f.outreach_id = $1
		ORDER BY f.created_at DESC, f.id DESC`, outreachID)
	if err != nil {
		return nil, fmt.Errorf("outreach: list flyers: %w", err)
	}
	defer rows.Close()
	out := []Flyer{}
	for rows.Next() {
		var f Flyer
		if err := rows.Scan(&f.ID, &f.OutreachID, &f.Photo, &f.FocalX, &f.FocalY, &f.Template, &f.Tagline, &f.CreatedAt, &f.UpdatedAt, &f.WorkshopID, &f.WorkshopSlug, &f.WorkshopTitle); err != nil {
			return nil, fmt.Errorf("outreach: scan flyer: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFlyer loads one flyer, or ErrFlyerNotFound.
func (s *Store) GetFlyer(ctx context.Context, id int64) (Flyer, error) {
	f, err := scanFlyer(s.pool.QueryRow(ctx, `SELECT `+flyerCols+` FROM outreach_flyers f WHERE f.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Flyer{}, ErrFlyerNotFound
	}
	return f, err
}

// CreateFlyer inserts a flyer.
func (s *Store) CreateFlyer(ctx context.Context, in FlyerInput) (Flyer, error) {
	return scanFlyer(s.pool.QueryRow(ctx, `
		WITH f AS (
		  INSERT INTO outreach_flyers (outreach_id, photo, focal_x, focal_y, template, tagline)
		  VALUES ($1, $2, $3, $4, $5, $6)
		  RETURNING *)
		SELECT `+flyerCols+` FROM f`, in.OutreachID, in.Photo, in.FocalX, in.FocalY, in.Template, in.Tagline))
}

// UpdateFlyer applies a partial update in one statement. updated_at moves
// only when something actually changed.
func (s *Store) UpdateFlyer(ctx context.Context, id int64, p FlyerPatch) (Flyer, error) {
	f, err := scanFlyer(s.pool.QueryRow(ctx, `
		WITH f AS (
		  UPDATE outreach_flyers SET
		    photo = CASE WHEN $2 THEN NULL ELSE COALESCE($3::text, photo) END,
		    focal_x = COALESCE($4::double precision, focal_x), focal_y = COALESCE($5::double precision, focal_y),
		    template = COALESCE($6::text, template), tagline = COALESCE($7::text, tagline),
		    updated_at = CASE WHEN
		      (CASE WHEN $2 THEN NULL ELSE COALESCE($3::text, photo) END) IS NOT DISTINCT FROM photo
		      AND COALESCE($4::double precision, focal_x) = focal_x AND COALESCE($5::double precision, focal_y) = focal_y
		      AND COALESCE($6::text, template) = template AND COALESCE($7::text, tagline) = tagline
		    THEN updated_at ELSE now() END
		  WHERE id = $1
		  RETURNING *)
		SELECT `+flyerCols+` FROM f`, id, p.ClearPhoto, p.Photo, p.FocalX, p.FocalY, p.Template, p.Tagline))
	if errors.Is(err, pgx.ErrNoRows) {
		return Flyer{}, ErrFlyerNotFound
	}
	return f, err
}

// DeleteFlyer deletes a flyer and its selection. Links stay: they belong to
// the outreach, not the flyer.
func (s *Store) DeleteFlyer(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM outreach_flyers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFlyerNotFound
	}
	return nil
}

// ListFlyerSelection returns the flyer's selected locations ordered by
// lower(name), id, each with the outreach's shared link when one exists.
func (s *Store) ListFlyerSelection(ctx context.Context, flyerID int64) ([]FlyerSelection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.name, l.code, l.address, l.active, fl.copies,
		       k.id, k.outreach_id, k.short_link_key, k.short_url, k.qr_svg IS NOT NULL, k.put_up_at
		FROM outreach_flyer_locations fl
		JOIN outreach_flyers f ON f.id = fl.flyer_id
		JOIN outreach_locations l ON l.id = fl.location_id
		LEFT JOIN outreach_flyer_links k ON k.outreach_id = f.outreach_id AND k.location_id = l.id
		WHERE fl.flyer_id = $1
		ORDER BY lower(l.name), l.id`, flyerID)
	if err != nil {
		return nil, fmt.Errorf("outreach: list flyer selection: %w", err)
	}
	defer rows.Close()
	out := []FlyerSelection{}
	for rows.Next() {
		var sel FlyerSelection
		var (
			lid, loid *int64
			key, surl *string
			hasQR     *bool
			putUp     *time.Time
		)
		if err := rows.Scan(&sel.LocationID, &sel.Name, &sel.Code, &sel.Address, &sel.Active, &sel.Copies,
			&lid, &loid, &key, &surl, &hasQR, &putUp); err != nil {
			return nil, fmt.Errorf("outreach: scan flyer selection: %w", err)
		}
		if lid != nil {
			sel.Link = &FlyerLink{ID: *lid, OutreachID: *loid, LocationID: sel.LocationID, Key: *key, ShortURL: *surl, HasQR: hasQR != nil && *hasQR, PutUpAt: putUp}
		}
		out = append(out, sel)
	}
	return out, rows.Err()
}

// ReplaceFlyerSelection replaces the flyer's selection in one transaction.
// An inactive location that is not already selected is ErrLocationInactive;
// a bad copy count, a repeated or unknown location, or more than
// MaxFlyerLocations entries is ErrFlyerSelectionInvalid.
func (s *Store) ReplaceFlyerSelection(ctx context.Context, flyerID int64, sel []SelectionInput) error {
	if len(sel) > MaxFlyerLocations {
		return ErrFlyerSelectionInvalid
	}
	seen := map[int64]bool{}
	for _, in := range sel {
		if in.Copies < MinFlyerCopies || in.Copies > MaxFlyerCopies || seen[in.LocationID] {
			return ErrFlyerSelectionInvalid
		}
		seen[in.LocationID] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT true FROM outreach_flyers WHERE id = $1 FOR UPDATE`, flyerID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFlyerNotFound
		}
		return err
	}
	already := map[int64]bool{}
	rows, err := tx.Query(ctx, `SELECT location_id FROM outreach_flyer_locations WHERE flyer_id = $1`, flyerID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		already[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, in := range sel {
		var active bool
		err := tx.QueryRow(ctx, `SELECT active FROM outreach_locations WHERE id = $1`, in.LocationID).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFlyerSelectionInvalid
		}
		if err != nil {
			return err
		}
		if !active && !already[in.LocationID] {
			return ErrLocationInactive
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outreach_flyer_locations WHERE flyer_id = $1`, flyerID); err != nil {
		return err
	}
	for _, in := range sel {
		if _, err := tx.Exec(ctx, `INSERT INTO outreach_flyer_locations (flyer_id, location_id, copies) VALUES ($1, $2, $3)`, flyerID, in.LocationID, in.Copies); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const flyerLinkCols = `id, outreach_id, location_id, short_link_key, short_url, qr_svg IS NOT NULL, put_up_at`

func scanFlyerLink(row pgx.Row) (FlyerLink, error) {
	var l FlyerLink
	err := row.Scan(&l.ID, &l.OutreachID, &l.LocationID, &l.Key, &l.ShortURL, &l.HasQR, &l.PutUpAt)
	return l, err
}

// GetFlyerLink loads one link, or ErrFlyerLinkNotFound.
func (s *Store) GetFlyerLink(ctx context.Context, id int64) (FlyerLink, error) {
	l, err := scanFlyerLink(s.pool.QueryRow(ctx, `SELECT `+flyerLinkCols+` FROM outreach_flyer_links WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return FlyerLink{}, ErrFlyerLinkNotFound
	}
	return l, err
}

// ListFlyerLinks returns an outreach's links in location order.
func (s *Store) ListFlyerLinks(ctx context.Context, outreachID int64) ([]FlyerLink, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+flyerLinkCols+` FROM outreach_flyer_links WHERE outreach_id = $1 ORDER BY location_id`, outreachID)
	if err != nil {
		return nil, fmt.Errorf("outreach: list flyer links: %w", err)
	}
	defer rows.Close()
	out := []FlyerLink{}
	for rows.Next() {
		l, err := scanFlyerLink(rows)
		if err != nil {
			return nil, fmt.Errorf("outreach: scan flyer link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListAllFlyerLinks returns every flyer link across all outreaches with the
// campaign and location context the results view needs.
func (s *Store) ListAllFlyerLinks(ctx context.Context) ([]FlyerLinkAll, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT k.id, k.outreach_id, k.location_id, k.short_link_key, k.short_url, k.qr_svg IS NOT NULL, k.put_up_at,
		       o.shortlinks_campaign_slug, o.created_at, w.starts_at, l.name, l.code
		FROM outreach_flyer_links k
		JOIN outreach o ON o.id = k.outreach_id
		JOIN workshops w ON w.id = o.workshop_id
		JOIN outreach_locations l ON l.id = k.location_id
		ORDER BY k.outreach_id, lower(l.name), l.id`)
	if err != nil {
		return nil, fmt.Errorf("outreach: list all flyer links: %w", err)
	}
	defer rows.Close()
	out := []FlyerLinkAll{}
	for rows.Next() {
		var a FlyerLinkAll
		if err := rows.Scan(&a.ID, &a.OutreachID, &a.LocationID, &a.Key, &a.ShortURL, &a.HasQR, &a.PutUpAt,
			&a.CampaignSlug, &a.OutreachCreatedAt, &a.WorkshopStartsAt, &a.LocationName, &a.Code); err != nil {
			return nil, fmt.Errorf("outreach: scan all flyer links: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// InsertFlyerLink records the link for (outreach, location) and locks the
// location's code, in one transaction. The location row is read FOR UPDATE
// first, so a code edited between building the destination and inserting is
// ErrLocationCodeChanged and nothing is written. When the pair already has a
// link the existing row is returned with created=false; MarkLocationUsed
// still runs, and is idempotent.
func (s *Store) InsertFlyerLink(ctx context.Context, outreachID, locationID int64, expectedCode, key, shortURL string) (FlyerLink, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FlyerLink{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var code string
	if err := tx.QueryRow(ctx, `SELECT code FROM outreach_locations WHERE id = $1 FOR UPDATE`, locationID).Scan(&code); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FlyerLink{}, false, ErrLocationNotFound
		}
		return FlyerLink{}, false, err
	}
	if code != expectedCode {
		return FlyerLink{}, false, ErrLocationCodeChanged
	}
	created := true
	l, err := scanFlyerLink(tx.QueryRow(ctx, `
		WITH k AS (
		  INSERT INTO outreach_flyer_links (outreach_id, location_id, short_link_key, short_url)
		  VALUES ($1, $2, $3, $4)
		  ON CONFLICT (outreach_id, location_id) DO NOTHING
		  RETURNING *)
		SELECT `+flyerLinkCols+` FROM k`, outreachID, locationID, key, shortURL))
	if errors.Is(err, pgx.ErrNoRows) {
		created = false
		l, err = scanFlyerLink(tx.QueryRow(ctx, `SELECT `+flyerLinkCols+` FROM outreach_flyer_links WHERE outreach_id = $1 AND location_id = $2`, outreachID, locationID))
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return FlyerLink{}, false, ErrNotFound
		}
		return FlyerLink{}, false, err
	}
	if err := s.MarkLocationUsed(ctx, tx, locationID); err != nil {
		return FlyerLink{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FlyerLink{}, false, err
	}
	return l, created, nil
}

// SetFlyerLinkQR caches the sanitized QR once; a link that already has one
// keeps it.
func (s *Store) SetFlyerLinkQR(ctx context.Context, id int64, svg []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE outreach_flyer_links SET qr_svg = $2 WHERE id = $1 AND qr_svg IS NULL`, id, string(svg))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := s.GetFlyerLink(ctx, id); gerr != nil {
			return gerr
		}
	}
	return nil
}

// FlyerLinkQR returns the cached QR and the location's code, or
// ErrFlyerLinkNotFound when the link or its QR does not exist yet.
func (s *Store) FlyerLinkQR(ctx context.Context, id int64) ([]byte, string, error) {
	var svg *string
	var code string
	err := s.pool.QueryRow(ctx, `
		SELECT k.qr_svg, l.code FROM outreach_flyer_links k
		JOIN outreach_locations l ON l.id = k.location_id WHERE k.id = $1`, id).Scan(&svg, &code)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && svg == nil) {
		return nil, "", ErrFlyerLinkNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return []byte(*svg), code, nil
}

// SetPutUp marks a link put up (keeping the first timestamp) or clears it,
// and returns the updated link.
func (s *Store) SetPutUp(ctx context.Context, id int64, putUp bool) (FlyerLink, error) {
	var q string
	if putUp {
		q = `UPDATE outreach_flyer_links SET put_up_at = COALESCE(put_up_at, now()) WHERE id = $1 RETURNING ` + flyerLinkCols
	} else {
		q = `UPDATE outreach_flyer_links SET put_up_at = NULL WHERE id = $1 RETURNING ` + flyerLinkCols
	}
	l, err := scanFlyerLink(s.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return FlyerLink{}, ErrFlyerLinkNotFound
	}
	return l, err
}
