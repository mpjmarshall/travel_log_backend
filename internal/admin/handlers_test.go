// The read pages: what they ask the store for, and what they draw.
package admin_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"travellog/internal/admin"
	"travellog/internal/media"
)

type fakeStore struct {
	overview   admin.Overview
	travellers []admin.Traveller
	total      int
	detail     admin.TravellerDetail
	sessions   []admin.Session
	invites    []admin.Invite
	err        error

	gotQuery  string
	gotLimit  int
	gotOffset int
	gotID     string
}

func (f *fakeStore) Overview(context.Context) (admin.Overview, error) {
	return f.overview, f.err
}

func (f *fakeStore) Travellers(_ context.Context, q string, limit, offset int) ([]admin.Traveller, int, error) {
	f.gotQuery, f.gotLimit, f.gotOffset = q, limit, offset
	return f.travellers, f.total, f.err
}

func (f *fakeStore) Traveller(_ context.Context, id string) (admin.TravellerDetail, error) {
	f.gotID = id
	if f.detail.ID == "" {
		return admin.TravellerDetail{}, errors.New("no such traveller")
	}
	return f.detail, f.err
}

func (f *fakeStore) Sessions(_ context.Context, id string) ([]admin.Session, error) {
	return f.sessions, f.err
}

func (f *fakeStore) Invites(context.Context) ([]admin.Invite, error) {
	return f.invites, f.err
}

func pageDeps(t *testing.T, store *fakeStore) (*http.ServeMux, *http.Cookie) {
	mux, c, _ := writeDeps(t, store, &fakeWriter{})
	return mux, c
}

func writeDeps(t *testing.T, store *fakeStore, writer *fakeWriter) (*http.ServeMux, *http.Cookie, string) {
	return writeDepsWithObjects(t, store, writer, nil)
}

func writeDepsWithObjects(t *testing.T, store *fakeStore, writer *fakeWriter,
	objects media.Store) (*http.ServeMux, *http.Cookie, string) {
	t.Helper()
	set, err := admin.NewTemplates(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	sessions := admin.NewSessions()
	deps := admin.Deps{
		Password: goodPassword,
		Sessions: sessions,
		Now:      now,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Render:   set,
		Store:    store,
		Writer:   writer,
		Objects:  objects,
	}
	mux := http.NewServeMux()
	admin.Mount(mux, deps)

	id, csrf, err := sessions.New(now())
	if err != nil {
		t.Fatal(err)
	}
	return mux, &http.Cookie{Name: admin.CookieName, Value: id}, csrf
}

func body(t *testing.T, mux *http.ServeMux, path string, c *http.Cookie, headers ...string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
		t.Fatalf("%s = %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestTheOverviewDrawsItsFigures(t *testing.T) {
	store := &fakeStore{overview: admin.Overview{
		Travellers: 13, Trips: 7, Photos: 286, Places: 16,
		LiveSessions: 4, UnusedInvites: 2, BucketBytes: 5_175_532,
	}}
	mux, c := pageDeps(t, store)

	got := body(t, mux, "/admin", c)
	for _, want := range []string{"13", "286", "Storage", "4.9 MB"} {
		if !strings.Contains(got, want) {
			t.Errorf("the overview does not show %q", want)
		}
	}
}

func TestTheTravellersListPassesTheSearchAndThePageThrough(t *testing.T) {
	store := &fakeStore{total: 3, travellers: []admin.Traveller{
		{ID: "id-1", Email: "ada@example.com", Trips: 2, CreatedAt: time.Now()},
	}}
	mux, c := pageDeps(t, store)

	body(t, mux, "/admin/travellers?q=ADA&offset=50", c)
	if store.gotQuery != "ADA" {
		t.Errorf("query reached the store as %q, want ADA", store.gotQuery)
	}
	if store.gotOffset != 50 {
		t.Errorf("offset reached the store as %d, want 50", store.gotOffset)
	}
	if store.gotLimit != admin.PageSize {
		t.Errorf("limit = %d, want PageSize %d", store.gotLimit, admin.PageSize)
	}
}

func TestAnHtmxRequestGetsTheRowsAndNotTheWholePage(t *testing.T) {
	store := &fakeStore{total: 1, travellers: []admin.Traveller{
		{ID: "id-1", Email: "ada@example.com", CreatedAt: time.Now()},
	}}
	mux, c := pageDeps(t, store)

	whole := body(t, mux, "/admin/travellers", c)
	fragment := body(t, mux, "/admin/travellers", c, "HX-Request", "true")

	if !strings.Contains(whole, "<nav>") {
		t.Error("the full page has no navigation")
	}
	if strings.Contains(fragment, "<nav>") {
		t.Error("an htmx swap returned the whole page, so searching would nest a " +
			"second navigation inside the table it replaced")
	}
	if !strings.Contains(fragment, "ada@example.com") {
		t.Error("the fragment does not carry the rows")
	}
}

func TestAnUnknownTravellerIsNotFoundRatherThanAnError(t *testing.T) {
	mux, c := pageDeps(t, &fakeStore{})

	req := httptest.NewRequest(http.MethodGet, "/admin/travellers/nobody", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown traveller = %d, want 404", rec.Code)
	}
}

func TestTheTravellerPageShowsCountsAndNeverAPhotograph(t *testing.T) {
	store := &fakeStore{detail: admin.TravellerDetail{
		Traveller: admin.Traveller{
			ID: "id-1", Email: "ada@example.com", Trips: 7, Photos: 286,
			CreatedAt: time.Now(),
		},
		Places: 16, BucketBytes: 5_175_532,
	}}
	mux, c := pageDeps(t, store)

	got := body(t, mux, "/admin/travellers/id-1", c)
	if !strings.Contains(got, "286") || !strings.Contains(got, "4.9 MB") {
		t.Error("the traveller page does not show its counts")
	}
	for _, forbidden := range []string{"<img", "presign", "X-Amz"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the page contains %q: the log browse is metadata only and must "+
				"never render a photograph or mint a URL", forbidden)
		}
	}
}

func TestAFailedReadSaysSoRatherThanDrawingNothing(t *testing.T) {
	store := &fakeStore{err: errors.New("the database is down")}
	mux, c := pageDeps(t, store)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a failed read = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "could not be read") {
		t.Error("a failed read drew no explanation")
	}
}

func TestTheInvitesPageSaysWhichAreUnused(t *testing.T) {
	store := &fakeStore{invites: []admin.Invite{
		{Note: "for matt", CreatedAt: time.Now()},
		{Note: "spent one", CreatedAt: time.Now(), Used: true, UsedBy: "ada@example.com"},
	}}
	mux, c := pageDeps(t, store)

	got := body(t, mux, "/admin/invites", c)
	if !strings.Contains(got, "unused") || !strings.Contains(got, "ada@example.com") {
		t.Error("the invites page does not distinguish spent from unused")
	}
}
