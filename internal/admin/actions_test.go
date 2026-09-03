// The writing routes: what reaches the writer, and what the page says back.
package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"time"

	"travellog/internal/admin"
	"travellog/internal/media"
)

type fakeWriter struct {
	renamedID        string
	renamedName      string
	mintedNote       string
	mintedHash       []byte
	deletedHash      []byte
	revokedID        string
	deletedTraveller string
	objects          []string
	err              error
}

func (f *fakeWriter) DeleteTraveller(_ context.Context, id string) ([]string, error) {
	f.deletedTraveller = id
	return f.objects, f.err
}

// fakeObjects stands in for the bucket, and records what it was asked to
// forget.
type fakeObjects struct {
	media.Memory
	deleted []media.Key
	err     error
}

func (f *fakeObjects) Delete(_ context.Context, key media.Key) error {
	f.deleted = append(f.deleted, key)
	return f.err
}

func adaDetail() admin.TravellerDetail {
	return admin.TravellerDetail{
		Traveller: admin.Traveller{
			ID: "id-1", Email: "ada@example.com", Trips: 7, Photos: 286,
			CreatedAt: time.Now(),
		},
		Places: 16, BucketBytes: 5_175_532,
	}
}

func (f *fakeWriter) Rename(_ context.Context, id, name string) (int64, error) {
	f.renamedID, f.renamedName = id, name
	return 2, f.err
}

func (f *fakeWriter) MintInvite(_ context.Context, hash []byte, note string) error {
	f.mintedHash, f.mintedNote = hash, note
	return f.err
}

func (f *fakeWriter) DeleteInvite(_ context.Context, hash []byte) error {
	f.deletedHash = hash
	return f.err
}

func (f *fakeWriter) RevokeSessionByID(_ context.Context, id string) error {
	f.revokedID = id
	return f.err
}

func postForm(t *testing.T, mux *http.ServeMux, path string, c *http.Cookie,
	csrf string, form string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	if csrf != "" {
		req.Header.Set(admin.CSRFHeader, csrf)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRenameReachesTheWriterWithTheTrimmedName(t *testing.T) {
	writer := &fakeWriter{}
	mux, c, csrf := writeDeps(t, &fakeStore{}, writer)

	rec := postForm(t, mux, "/admin/travellers/id-1/name", c, csrf, "name=++Ada+Lovelace++")
	if rec.Code >= 400 {
		t.Fatalf("rename = %d", rec.Code)
	}
	if writer.renamedID != "id-1" || writer.renamedName != "Ada Lovelace" {
		t.Errorf("writer got (%q, %q), want (id-1, Ada Lovelace)",
			writer.renamedID, writer.renamedName)
	}
}

func TestAMintedInviteIsShownOnceAndNeverStoredInPlaintext(t *testing.T) {
	writer := &fakeWriter{}
	mux, c, csrf := writeDeps(t, &fakeStore{}, writer)

	rec := postForm(t, mux, "/admin/invites", c, csrf, "note=for+matt")
	if rec.Code != http.StatusOK {
		t.Fatalf("mint = %d", rec.Code)
	}
	if writer.mintedNote != "for matt" {
		t.Errorf("note = %q", writer.mintedNote)
	}
	if len(writer.mintedHash) != 32 {
		t.Errorf("the writer was handed %d bytes, want a 32-byte hash: the plaintext "+
			"must never reach the store", len(writer.mintedHash))
	}
	if !strings.Contains(rec.Body.String(), "only time it is shown") {
		t.Error("the page does not say the code is shown once")
	}
}

func TestRevokingAnInviteDecodesItsHash(t *testing.T) {
	writer := &fakeWriter{}
	mux, c, csrf := writeDeps(t, &fakeStore{}, writer)

	hex := strings.Repeat("ab", 32)
	if rec := postForm(t, mux, "/admin/invites/"+hex+"/revoke", c, csrf, ""); rec.Code >= 400 {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if len(writer.deletedHash) != 32 || writer.deletedHash[0] != 0xab {
		t.Errorf("the writer got %x, want the decoded 32-byte hash", writer.deletedHash)
	}
}

func TestRevokingASessionPassesItsId(t *testing.T) {
	writer := &fakeWriter{}
	mux, c, csrf := writeDeps(t, &fakeStore{}, writer)

	if rec := postForm(t, mux, "/admin/sessions/session-9/revoke", c, csrf, ""); rec.Code >= 400 {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if writer.revokedID != "session-9" {
		t.Errorf("revoked %q, want session-9", writer.revokedID)
	}
}

func TestEveryWritingRouteRefusesWithoutTheCSRFToken(t *testing.T) {
	writer := &fakeWriter{}
	mux, c, _ := writeDeps(t, &fakeStore{}, writer)

	for _, path := range []string{
		"/admin/travellers/id-1/name",
		"/admin/invites",
		"/admin/invites/" + strings.Repeat("ab", 32) + "/revoke",
		"/admin/sessions/session-9/revoke",
	} {
		if code := postForm(t, mux, path, c, "", "").Code; code != http.StatusForbidden {
			t.Errorf("%s without a CSRF token = %d, want 403", path, code)
		}
	}
	if writer.renamedID != "" || writer.mintedNote != "" || writer.revokedID != "" {
		t.Error("a refused request still reached the writer")
	}
}

func TestAFailedWriteSaysSoAndIsNotSilent(t *testing.T) {
	writer := &fakeWriter{err: errors.New("the database is down")}
	mux, c, csrf := writeDeps(t, &fakeStore{}, writer)

	rec := postForm(t, mux, "/admin/travellers/id-1/name", c, csrf, "name=Ada")
	if rec.Code < 400 {
		t.Errorf("a failed rename answered %d, so the operator is told it worked", rec.Code)
	}
}

func TestDeleteRefusesUnlessTheTypedEmailMatchesExactly(t *testing.T) {
	store := &fakeStore{detail: adaDetail()}
	writer := &fakeWriter{}
	mux, c, csrf := writeDeps(t, store, writer)

	for _, typed := range []string{"", "ada@example.co", "ADA@EXAMPLE.COM", " "} {
		rec := postForm(t, mux, "/admin/travellers/id-1/delete", c, csrf, "email="+typed)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("typing %q = %d, want 400", typed, rec.Code)
		}
	}
	if writer.deletedTraveller != "" {
		t.Errorf("a refused confirmation still deleted %q", writer.deletedTraveller)
	}
}

func TestDeleteRemovesTheTravellerAndAsksTheBucketToForgetTheirObjects(t *testing.T) {
	store := &fakeStore{detail: adaDetail()}
	writer := &fakeWriter{objects: []string{"object-a", "object-b"}}
	objects := &fakeObjects{}
	mux, c, csrf := writeDepsWithObjects(t, store, writer, objects)

	rec := postForm(t, mux, "/admin/travellers/id-1/delete", c, csrf, "email=ada@example.com")
	if rec.Code >= 400 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if writer.deletedTraveller != "id-1" {
		t.Errorf("deleted %q, want id-1", writer.deletedTraveller)
	}
	if len(objects.deleted) != 2 {
		t.Errorf("the bucket was asked to forget %d objects, want 2: bytes left behind "+
			"are unreachable and still there after the account is gone", len(objects.deleted))
	}
}

func TestABucketFailureStillLeavesTheTravellerDeleted(t *testing.T) {
	store := &fakeStore{detail: adaDetail()}
	writer := &fakeWriter{objects: []string{"object-a"}}
	objects := &fakeObjects{err: errors.New("the bucket is unreachable")}
	mux, c, csrf := writeDepsWithObjects(t, store, writer, objects)

	rec := postForm(t, mux, "/admin/travellers/id-1/delete", c, csrf, "email=ada@example.com")
	if rec.Code >= 400 {
		t.Errorf("a bucket failure answered %d: the rows are already gone, so the "+
			"delete succeeded and only an orphan is left", rec.Code)
	}
	if writer.deletedTraveller != "id-1" {
		t.Error("the traveller was not deleted")
	}
}
