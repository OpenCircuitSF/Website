package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

type v1FlyerEnv struct {
	*v1OutreachWriteEnv
	fe *flyerEnv
}

// newV1FlyerEnv serves the #0703 operations over a flyer env with n
// locations and ShortLinks enabled.
func newV1FlyerEnv(t *testing.T, n int) *v1FlyerEnv {
	t.Helper()
	fe := newFlyerEnv(t, true, n)
	reg := v1TestRegistry(fe.pool, outreachFlyerOperations(fe.h)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(fe.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	write, tok := v1TestKey(t, fe.pool, apitokens.ScopeOutreachRead, apitokens.ScopeOutreachWrite)
	read, _ := v1TestKey(t, fe.pool, apitokens.ScopeOutreachRead)
	return &v1FlyerEnv{v1OutreachWriteEnv: &v1OutreachWriteEnv{outreachEnv: fe.outreachEnv, api: h, write: write, writeTok: tok, read: read}, fe: fe}
}

type v1FlyerResp struct {
	testFlyerView
	PrintURL string `json:"print_url"`
}

func TestV1OutreachFlyers_Flags(t *testing.T) {
	for _, op := range outreachFlyerOperations(nil) {
		d := op.Describe()
		destructive := strings.HasPrefix(d, "DESTRUCTIVE: ")
		if destructive != (op.Name == "delete_flyer") || strings.Contains(d, "PUBLIC:") {
			t.Errorf("%s description %q", op.Name, d)
		}
	}
}

func TestV1OutreachFlyers_CRUD(t *testing.T) {
	e := newV1FlyerEnv(t, 2)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	body := fmt.Sprintf(`{"outreach_id":%d,"template":"split","tagline":"  Come solder  ","photo":"/media/a.jpg"}`, o.ID)

	e.v1(t, http.MethodPost, "/api/v1/flyers", e.read, body, http.StatusForbidden)
	for _, bad := range []string{
		fmt.Sprintf(`{"outreach_id":%d,"photo":"/uploads/a.jpg"}`, o.ID),
		fmt.Sprintf(`{"outreach_id":%d,"photo":"https://evil.example/a.jpg"}`, o.ID),
		fmt.Sprintf(`{"outreach_id":%d,"photo":"/media/a\u0001.jpg"}`, o.ID),
	} {
		if b := e.v1(t, http.MethodPost, "/api/v1/flyers", e.write, bad, http.StatusBadRequest); !strings.Contains(string(b), `"photo"`) {
			t.Errorf("bad photo %s: %s", bad, b)
		}
	}
	if b := e.v1(t, http.MethodPost, "/api/v1/flyers", e.write, fmt.Sprintf(`{"outreach_id":%d,"template":"nope"}`, o.ID), http.StatusBadRequest); !strings.Contains(string(b), `"template"`) {
		t.Errorf("bad template: %s", b)
	}
	e.v1(t, http.MethodPost, "/api/v1/flyers", e.write, `{"outreach_id":999999999}`, http.StatusNotFound)

	b := e.v1(t, http.MethodPost, "/api/v1/flyers", e.write, body, http.StatusCreated, "Idempotency-Key", fmt.Sprintf("flyer-0703-%d", o.ID))
	assertPacific(t, b)
	var v v1FlyerResp
	if err := json.Unmarshal(b, &v); err != nil || v.Flyer.Tagline != "Come solder" || v.Flyer.Photo == nil || *v.Flyer.Photo != "/media/a.jpg" {
		t.Fatalf("create %s (%v)", b, err)
	}
	if want := fmt.Sprintf("/admin#/outreach?view=print&flyer=%d&layout=full", v.Flyer.ID); !strings.HasSuffix(v.PrintURL, want) {
		t.Errorf("print_url %q, want suffix %q", v.PrintURL, want)
	}
	again := e.v1(t, http.MethodPost, "/api/v1/flyers", e.write, body, http.StatusCreated, "Idempotency-Key", fmt.Sprintf("flyer-0703-%d", o.ID))
	if string(again) != string(b) {
		t.Errorf("idempotent replay differs:\n%s\n%s", b, again)
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerCreated, 1)

	list := e.v1(t, http.MethodGet, "/api/v1/flyers", e.read, "", http.StatusOK)
	assertPacific(t, list)
	if !strings.Contains(string(list), fmt.Sprintf(`"id":%d`, v.Flyer.ID)) {
		t.Errorf("list missing flyer: %s", list)
	}
	path := fmt.Sprintf("/api/v1/flyers/%d", v.Flyer.ID)
	assertPacific(t, e.v1(t, http.MethodGet, path, e.read, "", http.StatusOK))
	e.v1(t, http.MethodGet, "/api/v1/flyers/999999999", e.read, "", http.StatusNotFound)

	e.v1(t, http.MethodPatch, path, e.write, `{}`, http.StatusBadRequest)
	e.v1(t, http.MethodPatch, path, e.write, `{"photo":"/x.jpg"}`, http.StatusBadRequest)
	pb := e.v1(t, http.MethodPatch, path, e.write, `{"photo":"","tagline":"New"}`, http.StatusOK)
	assertPacific(t, pb)
	if err := json.Unmarshal(pb, &v); err != nil || v.Flyer.Photo != nil || v.Flyer.Tagline != "New" {
		t.Errorf("patch %s", pb)
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerUpdated, 1)

	lp := path + "/locations"
	e.v1(t, http.MethodPut, lp, e.write, `{"locations":[{"location_id":999999999,"copies":1}]}`, http.StatusBadRequest)
	sb := e.v1(t, http.MethodPut, lp, e.write, fmt.Sprintf(`{"locations":[{"location_id":%d,"copies":3}]}`, e.fe.locs[0].ID), http.StatusOK)
	if err := json.Unmarshal(sb, &v); err != nil || len(v.Selection) != 1 || v.Selection[0].Copies != 3 {
		t.Errorf("set locations %s", sb)
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerUpdated, 2)

	e.v1(t, http.MethodDelete, path, e.read, "", http.StatusForbidden)
	if db := e.v1(t, http.MethodDelete, path, e.write, "", http.StatusOK); !strings.Contains(string(db), `"deleted":true`) {
		t.Errorf("delete %s", db)
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerDeleted, 1)
	e.v1(t, http.MethodDelete, path, e.write, "", http.StatusNotFound)
}

func TestV1OutreachFlyers_GenerateAndPutUp(t *testing.T) {
	e := newV1FlyerEnv(t, 2)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.fe.flyerFor(t, w, e.fe.locs[0], e.fe.locs[1])
	gp := fmt.Sprintf("/api/v1/flyers/%d/generate", v.Flyer.ID)

	e.v1(t, http.MethodPost, gp, e.read, "", http.StatusForbidden)
	b := e.v1(t, http.MethodPost, gp, e.write, "", http.StatusOK)
	assertPacific(t, b)
	var got v1FlyerResp
	if err := json.Unmarshal(b, &got); err != nil || got.Remaining == nil || *got.Remaining != 0 || len(got.Selection) != 2 {
		t.Fatalf("generate %s (%v)", b, err)
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerLinkCreated, 2)
	e.v1(t, http.MethodPost, gp, e.write, "", http.StatusOK)
	if len(e.links.links) != 2 {
		t.Errorf("second generate made links: %d upstream", len(e.links.links))
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerLinkCreated, 2)

	id := got.Selection[0].Link.ID
	pp := fmt.Sprintf("/api/v1/flyer-links/%d/put-up", id)
	up := e.v1(t, http.MethodPost, pp, e.write, `{"put_up":true}`, http.StatusOK)
	assertPacific(t, up)
	var l flyerLinkView
	if err := json.Unmarshal(up, &l); err != nil || l.PutUpAt == nil || !strings.Contains(*l.PutUpAt, "-0") {
		t.Fatalf("put up %s", up)
	}
	e.v1(t, http.MethodPost, pp, e.write, `{"put_up":true}`, http.StatusOK)
	e.assertTokenAudit(t, audit.ActionOutreachFlyerLinkPutUp, 1)
	down := e.v1(t, http.MethodPost, pp, e.write, `{"put_up":false}`, http.StatusOK)
	if err := json.Unmarshal(down, &l); err != nil || l.PutUpAt != nil {
		t.Errorf("clear %s", down)
	}
	e.assertTokenAudit(t, audit.ActionOutreachFlyerLinkPutUpCleared, 1)
	e.v1(t, http.MethodPost, "/api/v1/flyer-links/999999999/put-up", e.write, `{"put_up":true}`, http.StatusNotFound)
}

func TestV1OutreachFlyers_GenerateRefusals(t *testing.T) {
	e := newV1FlyerEnv(t, 1)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	v := e.fe.flyerFor(t, w)
	b := e.v1(t, http.MethodPost, fmt.Sprintf("/api/v1/flyers/%d/generate", v.Flyer.ID), e.write, "", http.StatusBadRequest)
	if !strings.Contains(string(b), `"locations"`) {
		t.Errorf("empty selection %s", b)
	}
	e.v1(t, http.MethodPost, "/api/v1/flyers/999999999/generate", e.write, "", http.StatusNotFound)
}
