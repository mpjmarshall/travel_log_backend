// `GET /l/{token}` over the real mux, the real middleware chain and the real
// route table.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"travellog/internal/httpx"
	"travellog/internal/logbook"
	"travellog/internal/media"
)

// theToken is what the tests mint.
const theToken = "mnpqrstuvwxy"

// theAllowlist is docs/PUBLIC-ENVELOPE.md §3, typed out.
var theAllowlist = map[string][]string{
	"":                     {"cities", "photos", "places", "trip", "version", "walks"},
	"trip":                 {"cityIds", "coverUrl", "end", "id", "name", "start", "summary"},
	"cities[]":             {"centre", "country", "id", "name"},
	"cities[].centre":      {"lat", "lng"},
	"cities[].country":     {"code", "name"},
	"places[]":             {"cityId", "coordinates", "days", "id", "name"},
	"places[].coordinates": {"lat", "lng"},
	"places[].days[]":      {"at", "note"},
	"photos[]": {"accuracyMetres", "caption", "cityId", "coordinates", "id",
		"placeId", "takenAt", "url"},
	"photos[].coordinates": {"lat", "lng"},
	"walks[]":              {"cityId", "distanceKm", "id", "points", "recordedOn"},
	"walks[].points[]":     {"lat", "lng"},
}

// keyPaths walks a decoded document and answers "path -> the keys seen
// there", merged across every element of every list.
func keyPaths(value any, at string, into map[string]map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		if into[at] == nil {
			into[at] = map[string]bool{}
		}
		for key, child := range v {
			into[at][key] = true
			next := key
			if at != "" {
				next = at + "." + key
			}
			keyPaths(child, next, into)
		}
	case []any:
		for _, child := range v {
			keyPaths(child, at+"[]", into)
		}
	}
}

func walkOf(t *testing.T, body []byte) map[string][]string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the public envelope is not JSON: %v", err)
	}
	seen := map[string]map[string]bool{}
	keyPaths(doc, "", seen)

	out := map[string][]string{}
	for path, keys := range seen {
		var flat []string
		for key := range keys {
			flat = append(flat, key)
		}
		sort.Strings(flat)
		out[path] = flat
	}
	return out
}

// renderWalk sorts both the paths and the keys.
func renderWalk(walk map[string][]string) string {
	var paths []string
	for path := range walk {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, path := range paths {
		name := path
		if name == "" {
			name = "(root)"
		}
		keys := slices.Clone(walk[path])
		sort.Strings(keys)
		fmt.Fprintf(&b, "%s: %s\n", name, strings.Join(keys, " "))
	}
	return b.String()
}

// the structural guard (the other half, and the whole point).
func TestThePublicEnvelopeCarriesExactlyTheAllowlistAtEveryLevel(t *testing.T) {
	h := newHarness(t, options{})
	h.setFlags(t, `{"shareCoordinates":true}`)
	body := h.sharedTrip(t, theToken)

	walk := walkOf(t, body)

	for path, want := range theAllowlist {
		got, held := walk[path]
		if !held {
			t.Errorf("the allowlist names %q and the document has no such level — "+
				"either the envelope lost a level or the fixture never had one",
				levelName(path))
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s\n     carries: %v\n     allowed: %v",
				levelName(path), got, want)
		}
	}
	for path, got := range walk {
		if _, held := theAllowlist[path]; !held {
			t.Errorf("the document has a level the allowlist does not name: %s carrying %v.\n"+
				"    Every key reaches this envelope by being written down in\n"+
				"    docs/PUBLIC-ENVELOPE.md §3, never by being harmless.",
				levelName(path), got)
		}
	}

	golden(t, "share_all_on.golden", renderWalk(walk))
	golden(t, "share_allowlist.golden", renderWalk(theAllowlist))
}

func levelName(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}

// The scalpel: with shareCoordinates off, the only `lat` left is the
// city's centre.
func TestWithCoordinatesOffTheOnlyLatLeftIsTheCitysCentre(t *testing.T) {
	h := newHarness(t, options{})
	h.setFlags(t, `{"shareCoordinates":false}`)
	body := h.sharedTrip(t, theToken)

	var withLat []string
	for path, keys := range walkOf(t, body) {
		if slices.Contains(keys, "lat") {
			withLat = append(withLat, path)
		}
	}
	sort.Strings(withLat)

	if !slices.Equal(withLat, []string{"cities[].centre"}) {
		t.Errorf("the levels carrying a `lat` are %v, want exactly [cities[].centre].\n"+
			"    A city centre is coarse — it IS a city — and it is what a map opens\n"+
			"    on with no pins to fit. Everything else that carries one is a place\n"+
			"    somebody stood.", withLat)
	}
	golden(t, "share_coordinates_off.golden", renderWalk(walkOf(t, body)))
}

// share photos off, over the wire: the key SET does not change and the
// CONTENT does.
func TestWithPhotosOffTheArrayIsEmptyAndTheCoverIsNull(t *testing.T) {
	h := newHarness(t, options{})
	h.setFlags(t, `{"sharePhotos":false}`)
	body := h.sharedTrip(t, theToken)

	var doc struct {
		Trip struct {
			CoverURL *string `json:"coverUrl"`
		} `json:"trip"`
		Photos []any `json:"photos"`
		Places []any `json:"places"`
		Walks  []any `json:"walks"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Photos == nil {
		t.Error("photos is null over the wire, want [] — `null` is neither an absent " +
			"key nor an empty list, and it is the one shape a List<dynamic> cast throws on")
	}
	if len(doc.Photos) != 0 {
		t.Errorf("photos carries %d entries with sharePhotos off", len(doc.Photos))
	}
	if doc.Trip.CoverURL != nil {
		t.Errorf("trip.coverUrl = %q with sharePhotos off", *doc.Trip.CoverURL)
	}
	if len(doc.Places) == 0 || len(doc.Walks) == 0 {
		t.Errorf("the rest of the document went with the photographs: %d places, %d walks",
			len(doc.Places), len(doc.Walks))
	}
	if !strings.Contains(string(body), `"photos":[]`) {
		t.Errorf("the raw bytes do not carry an empty photos array: %s", body)
	}
}

// BYTE-IDENTICAL **and** the same work.
func TestARevokedTokenAndAnUnknownOneAreOneAnswerAndOneAmountOfWork(t *testing.T) {
	h := newHarness(t, options{})
	h.sharedTrip(t, theToken) // mint it, then kill it
	if r := h.do(t, http.MethodDelete, "/v1/trips/kyoto/share", "", h.token(t)); r.status != 200 {
		t.Fatalf("DELETE share = %d: %s", r.status, r.body)
	}

	h.public.reset()
	h.objectCounter().reset()
	revoked := h.getPublic(t, theToken)
	revokedWork := h.work()

	h.public.reset()
	h.objectCounter().reset()
	unknown := h.getPublic(t, "abcdefghjkmn")
	unknownWork := h.work()

	if revoked.status != http.StatusNotFound || unknown.status != http.StatusNotFound {
		t.Fatalf("revoked = %d, unknown = %d, want 404 and 404", revoked.status, unknown.status)
	}
	if string(revoked.body) != string(unknown.body) {
		t.Errorf("revoked body %s, unknown body %s — a different body is a plain oracle "+
			"for which tokens once existed", revoked.body, unknown.body)
	}
	if got, want := headersFor(revoked), headersFor(unknown); got != want {
		t.Errorf("revoked headers\n  %s\nunknown headers\n  %s", got, want)
	}

	if revokedWork != unknownWork {
		t.Errorf("a revoked token cost %+v and an unknown one cost %+v.\n"+
			"    Equal bytes and unequal work is still an oracle for 'this token was\n"+
			"    once real': the lookup has to be the ONLY branch.",
			revokedWork, unknownWork)
	}
	if revokedWork.lookups != 1 {
		t.Errorf("a revoked token cost %d share-link lookups, want 1 — this leg is not "+
			"measuring anything", revokedWork.lookups)
	}
	if revokedWork.reads != 0 || revokedWork.mints != 0 {
		t.Errorf("a token with no live link cost %d log reads and %d mints, want 0 and 0",
			revokedWork.reads, revokedWork.mints)
	}
}

// asserted by presence and not by sameness.
func TestThePublicReadCarriesTheCapabilityHeadersOnEveryAnswer(t *testing.T) {
	h := newHarness(t, options{})

	for _, tc := range []struct {
		name   string
		answer answer
		status int
	}{
		{"the envelope", h.getPublicRaw(t, theToken, true), http.StatusOK},
		{"a token nobody holds", h.getPublic(t, "abcdefghjkmn"), http.StatusNotFound},
	} {
		if tc.answer.status != tc.status {
			t.Fatalf("%s = %d, want %d: %s", tc.name, tc.answer.status, tc.status, tc.answer.body)
		}
		if got := tc.answer.header.Get("Cache-Control"); got != "no-store, private" {
			t.Errorf("%s: Cache-Control = %q, want \"no-store, private\".\n"+
				"    This route carries no Authorization header, so RFC 9111 §3.5's\n"+
				"    shared-cache prohibition — the thing silently protecting\n"+
				"    GET /v1/logbook — does not reach it. A cached envelope keeps\n"+
				"    handing out live media capabilities after 'Stop sharing'.",
				tc.name, got)
		}
		if got := tc.answer.header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q, want \"no-referrer\".\n"+
				"    A presigned URL is a pure bearer capability; without this it\n"+
				"    travels in a Referer header into the next origin's access log.",
				tc.name, got)
		}
	}
}

// every minted url uses the public lifetime, asserted by call site.
func TestEveryURLTheEnvelopeEmbedsIsMintedAtThePublicLifetime(t *testing.T) {
	const publicSeconds = "900" // fifteen minutes, DEC-84
	const privateSeconds = "120"

	h := newHarness(t, options{})
	body := h.sharedTrip(t, theToken)

	var doc struct {
		Trip struct {
			CoverURL *string `json:"coverUrl"`
		} `json:"trip"`
		Photos []struct {
			URL string `json:"url"`
		} `json:"photos"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	urls := []string{}
	if doc.Trip.CoverURL != nil {
		urls = append(urls, *doc.Trip.CoverURL)
	}
	for _, photo := range doc.Photos {
		urls = append(urls, photo.URL)
	}
	if len(urls) == 0 {
		t.Fatal("the envelope embeds no URLs at all, so this leg is measuring nothing")
	}

	for _, raw := range urls {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("the envelope embedded an unparseable URL %q: %v", raw, err)
		}
		got := parsed.Query().Get("X-Amz-Expires")
		if got == privateSeconds {
			t.Errorf("a URL in the PUBLIC envelope expires in %ss — that is the PRIVATE "+
				"lifetime (DEC-44), the phone's own revocation knob. This envelope has "+
				"nothing to re-mint with: the reader holds no credential and "+
				"POST /v1/media/mint is authenticated, so a share page would die "+
				"mid-scroll.", got)
		}
		if got != publicSeconds {
			t.Errorf("X-Amz-Expires = %q, want %q (DEC-84's fifteen minutes)", got, publicSeconds)
		}
	}
}

// exhausting the public read does not refuse A SIGN-IN.
func TestExhaustingThePublicReadDoesNotRefuseASignIn(t *testing.T) {
	h := newHarness(t, options{publicPerMin: 2})
	h.sharedTrip(t, theToken)

	refused := 0
	for range 6 {
		if h.getPublic(t, theToken).status == http.StatusTooManyRequests {
			refused++
		}
	}
	if refused == 0 {
		t.Fatal("six public reads at a ceiling of two produced no 429 — the public read " +
			"has no ceiling at all, which is unmetered token enumeration on the one " +
			"route with no credential in front of it")
	}

	if got := h.post(t, "/v1/auth/session", registered); got.status == http.StatusTooManyRequests {
		t.Errorf("signing in answered 429 after the PUBLIC read's bucket was spent.\n"+
			"    One person browsing a shared trip has locked everybody out of the\n"+
			"    application: the two ceilings are one bucket. (%s)", got.body)
	}
}

// countingObjects wraps the real media twin and counts read mints.
type countingObjects struct {
	media.Store
	mints int
}

func (c *countingObjects) PresignGet(ctx context.Context, key media.Key, aud media.Audience) (string, error) {
	c.mints++
	return c.Store.PresignGet(ctx, key, aud)
}

func (c *countingObjects) reset() { c.mints = 0 }

// fakePublic is a logbook.PublicStore over one fixed document.
type fakePublic struct {
	books   *fakeLogbook
	lookups int
	reads   int
}

func (f *fakePublic) reset() { f.lookups, f.reads = 0, 0 }

func (f *fakePublic) ShareLink(_ context.Context, tokenHash []byte) (logbook.ShareLink, error) {
	f.books.mu.Lock()
	defer f.books.mu.Unlock()
	f.lookups++
	for _, link := range f.books.links {
		if link.hash == string(tokenHash) {
			return logbook.ShareLink{
				TravellerID: link.travellerID, TripID: link.tripID, Revoked: link.revoked,
			}, nil
		}
	}
	return logbook.ShareLink{}, logbook.ErrNoShare
}

func (f *fakePublic) PublicLog(_ context.Context, _, tripID string) (logbook.PublicSource, error) {
	f.books.mu.Lock()
	defer f.books.mu.Unlock()
	f.reads++

	var src logbook.PublicSource
	found := false
	for _, trip := range f.books.doc.Trips {
		if trip.ID == tripID {
			src.Trip, found = trip, true
		}
	}
	if !found {
		return logbook.PublicSource{}, fmt.Errorf("%w: %s", logbook.ErrNoTrip, tripID)
	}

	for _, id := range src.Trip.CityIDs {
		for _, city := range f.books.doc.Cities {
			if city.ID == id {
				src.Cities = append(src.Cities, city)
			}
		}
	}
	for _, place := range f.books.doc.Places {
		var mine []logbook.Visit
		for _, visit := range place.Visits {
			if visit.TripID == tripID {
				mine = append(mine, visit)
			}
		}
		if len(mine) == 0 {
			continue
		}
		place.Visits = mine
		src.Places = append(src.Places, place)
	}
	for _, photo := range f.books.doc.Photos {
		if photo.TripID == tripID {
			src.Photos = append(src.Photos, photo)
		}
	}
	for _, walk := range f.books.doc.Walks {
		if walk.TripID == tripID && !walk.Dismissed {
			src.Walks = append(src.Walks, walk)
		}
	}
	return src, nil
}

type publicWork struct{ lookups, reads, mints int }

func (h *harness) objectCounter() *countingObjects {
	return h.deps.Objects.(*countingObjects)
}

func (h *harness) work() publicWork {
	return publicWork{
		lookups: h.public.lookups,
		reads:   h.public.reads,
		mints:   h.objectCounter().mints,
	}
}

func (h *harness) getPublic(t *testing.T, token string) answer {
	t.Helper()
	return h.do(t, http.MethodGet, "/l/"+token, "", "")
}

// getPublicRaw mints the link first when asked, so a header leg does not have
// to repeat the setup.
func (h *harness) getPublicRaw(t *testing.T, token string, mint bool) answer {
	t.Helper()
	if mint {
		h.sharedTrip(t, token)
	}
	return h.getPublic(t, token)
}

// token registers once and answers a bearer, memoised on the harness's own
// store so repeated calls do not spend two more credential tokens.
func (h *harness) token(t *testing.T) string {
	t.Helper()
	if h.bearerToken == "" {
		h.bearerToken = bearer(t, h)
	}
	return h.bearerToken
}

// sharedTrip builds a trip with one of everything the envelope can carry,
// shares it, and answers the public body.
func (h *harness) sharedTrip(t *testing.T, token string) []byte {
	t.Helper()
	bearerToken := h.token(t)

	if h.logbook.doc.Trips == nil {
		if got := h.put(t, "/v1/trips/kyoto", aTrip, bearerToken); got.status != http.StatusOK {
			t.Fatalf("PUT /v1/trips/kyoto = %d: %s", got.status, got.body)
		}
		h.fillTheLog(t)
	}
	if got := h.do(t, http.MethodPost, "/v1/trips/kyoto/share",
		`{"token":"`+token+`"}`, bearerToken); got.status != http.StatusCreated {
		t.Fatalf("POST share = %d: %s", got.status, got.body)
	}

	got := h.getPublic(t, token)
	if got.status != http.StatusOK {
		t.Fatalf("GET /l/%s = %d: %s", token, got.status, got.body)
	}
	return got.body
}

// setFlags flicks one switch through H1's own route.
func (h *harness) setFlags(t *testing.T, body string) {
	t.Helper()
	bearerToken := h.token(t)
	if h.logbook.doc.Trips == nil {
		if got := h.put(t, "/v1/trips/kyoto", aTrip, bearerToken); got.status != http.StatusOK {
			t.Fatalf("PUT /v1/trips/kyoto = %d: %s", got.status, got.body)
		}
		h.fillTheLog(t)
	}
	if got := h.put(t, "/v1/trips/kyoto/share", body, bearerToken); got.status != http.StatusOK {
		t.Fatalf("PUT share %s = %d: %s", body, got.status, got.body)
	}
}

// fillTheLog puts one of everything the envelope can carry into the twin's
// document, with every flag-sensitive field populated.
func (h *harness) fillTheLog(t *testing.T) {
	t.Helper()
	h.logbook.mu.Lock()
	defer h.logbook.mu.Unlock()

	moment, err := time.Parse(time.RFC3339, fixedNow)
	if err != nil {
		t.Fatalf("parsing the pinned clock: %v", err)
	}
	at := logbook.At(moment)
	note, caption := "the torii went on for ever", "the last light on the ridge"
	summary := "two weeks, five cities"
	placeID := "fushimi-inari"
	accuracy := 12
	cover := strings.Repeat("a", 64)

	for i := range h.logbook.doc.Trips {
		if h.logbook.doc.Trips[i].ID != "kyoto" {
			continue
		}
		h.logbook.doc.Trips[i].CityIDs = []string{"kyoto"}
		h.logbook.doc.Trips[i].Start, h.logbook.doc.Trips[i].End = &at, &at
		h.logbook.doc.Trips[i].Summary = &summary
		h.logbook.doc.Trips[i].CoverAsset = &cover
	}
	h.logbook.doc.Cities = []logbook.City{{
		ID: "kyoto", Name: "Kyoto",
		Country: logbook.Country{Code: "JP", Name: "Japan"},
		Centre:  logbook.LatLng{Lat: 35.0116, Lng: 135.7681},
	}}
	h.logbook.doc.Places = []logbook.Place{
		{
			ID: placeID, CityID: "kyoto", Name: "Fushimi Inari",
			Coordinates: logbook.LatLng{Lat: 34.9671, Lng: 135.7727},
			Visits: []logbook.Visit{
				{ID: "v-0", PlaceID: placeID, TripID: "kyoto", At: at, Note: &note},
				{ID: "v-other", PlaceID: placeID, TripID: "elsewhere", At: at, Note: &note},
			},
		},
		{ID: "tofuku-ji", CityID: "kyoto", Name: "Tofuku-ji",
			Coordinates: logbook.LatLng{Lat: 34.97, Lng: 135.77}},
	}
	h.logbook.doc.Photos = []logbook.Photo{{
		ID: "ph-0", TripID: "kyoto", CityID: "kyoto", TakenAt: at,
		Asset: strings.Repeat("b", 64), PlaceID: &placeID, VisitID: strPtr("v-0"),
		Caption:     &caption,
		Coordinates: &logbook.LatLng{Lat: 34.9671, Lng: 135.7727}, AccuracyMetres: &accuracy,
	}}
	h.logbook.doc.Walks = []logbook.Walk{
		{ID: "w-0", TripID: "kyoto", CityID: "kyoto", RecordedOn: at, DistanceKm: 6.4,
			Points: []logbook.LatLng{{Lat: 34.9, Lng: 135.7}, {Lat: 34.91, Lng: 135.71}},
			Name:   strPtr("the long way round")},
		{ID: "w-discarded", TripID: "kyoto", CityID: "kyoto", RecordedOn: at, DistanceKm: 0.4,
			Points: []logbook.LatLng{{Lat: 34.9, Lng: 135.7}}, Dismissed: true},
	}
}

func strPtr(s string) *string { return &s }

func headersFor(a answer) string {
	var names []string
	for name := range a.header {
		if name == httpx.RequestIDHeader || name == "Date" {
			continue
		}
		names = append(names, name+": "+a.header.Get(name))
	}
	sort.Strings(names)
	return strings.Join(names, "\n  ")
}

// golden compares against internal/httpapi/testdata and rewrites with
// -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\n\nwhat the code produced:\n%s", path, err, got)
	}
	if string(want) != got {
		t.Errorf("%s does not match.\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}
