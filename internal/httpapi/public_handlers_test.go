// `GET /l/{token}` over the real mux, the real middleware chain and the real
// route table.
//
// WHAT ONLY THIS CAN SAY. internal/logbook's legs are about keys and flags and
// internal/postgres's are about rows; neither can see a status, a header, or
// the two answers that have to be one answer. What leaves the process is this
// package's subject, and on this route what leaves the process is the whole
// security question of the step.
//
// THE STRUCTURAL WALK IS HERE AND NOT A SUBSTRING SEARCH, and
// docs/PUBLIC-ENVELOPE.md §1 says why: with `shareCoordinates` false the claim
// is "the only `lat` in this document is under cities[].centre", and a
// `grep -c lat` would have to be zero — which is WRONG, because the centre
// stays. It is a PATH claim rather than a WORD claim and only a walk can make
// it.
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

// theToken is what the tests mint. Twelve characters of the client's own
// alphabet, so it is a token `POST /v1/trips/{id}/share` accepts.
const theToken = "mnpqrstuvwxy"

// -------------------------------------------------------------- THE ALLOWLIST

// theAllowlist is docs/PUBLIC-ENVELOPE.md §3, typed out.
//
// IT IS THE AUTHORITY AND THE GOLDEN IS NOT. A golden file records what the
// code emitted; regenerate it after a mistake and it records the mistake. This
// map is read off the specification, so the leg below asserts THREE things
// against each other — the response, the golden, and this — and a golden
// regenerated to match a wrong answer reddens.
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

// keyPaths walks a decoded document and answers "path -> the keys seen there",
// merged across every element of every list.
//
// MERGED ACROSS ELEMENTS ON PURPOSE. A photograph with a null coordinate and
// one with a coordinate carry the same KEYS — nothing here is `omitempty` — so
// the union is the object's shape rather than a sample of it, and a key that
// appears on one row out of ninety-six is still a key on the allowlist or not.
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

// renderWalk sorts BOTH the paths and the keys, so the golden is a fact about
// the SET at each level rather than about the order a map happened to answer
// in — and so the allowlist above can be typed out in whatever order reads
// best without moving a golden.
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

// THE STRUCTURAL GUARD (PD-07's other half, and DEC-24's whole point).
//
// Parse, walk, and assert the key set AT EVERY LEVEL equals the allowlist. Not
// "contains", not "does not contain the bad ones" — EQUALS, in both
// directions, because an allowlist that only checks for known-bad keys is a
// deny-list somebody has to remember to add to, and every field this project
// adds after today would be published by default.
func TestThePublicEnvelopeCarriesExactlyTheAllowlistAtEveryLevel(t *testing.T) {
	h := newHarness(t, options{})
	// ALL THREE SWITCHES ON, AND THE THIRD ONE HAS TO BE ASKED FOR. Migration
	// 0002's defaults are the CLIENT's — true, true, FALSE — because a pin on
	// your accommodation is not something to hand out by link, so it has to be
	// actively turned on every time. Without this line the document under test
	// is the coordinates-off one and three levels of the allowlist are simply
	// absent, which is what this leg first said when it was written.
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

	// THE GOLDEN IS COMPARED TO THE ALLOWLIST AS WELL AS TO THE RESPONSE, so a
	// golden regenerated to match a mistake reddens instead of blessing it.
	golden(t, "share_all_on.golden", renderWalk(walk))
	golden(t, "share_allowlist.golden", renderWalk(theAllowlist))
}

func levelName(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}

// AND THE SCALPEL: with shareCoordinates off, the only `lat` left is the
// city's centre.
//
// THIS IS THE LEG THAT PROVES THE FILTER IS NOT A BLANKET, and it is the exact
// claim §1 says a substring search cannot make. `grep -c lat` would have to be
// zero and zero is the wrong answer.
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

// SHARE PHOTOS OFF, OVER THE WIRE: the key SET does not change and the CONTENT
// does.
//
// The keys are the same because `photos` is an EMPTY ARRAY rather than a
// missing key — a reader that branches on presence has three states to handle
// and one that branches on length has two — so this leg is about rows and
// about `coverUrl`, and the golden records both.
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

// ------------------------------------------------- REVOKED, UNKNOWN, AND WORK

// BYTE-IDENTICAL **AND** THE SAME WORK (PD-12, DEC-10).
//
// DEC-10 says "the SAME 404 WITH THE SAME WORK DONE". Byte-identical is
// necessary and is not sufficient: a handler that returned early on "no row"
// but, for a revoked row, resolved the trip, read three flags and minted a
// dozen URLs would answer identical bytes and still be a clean oracle for
// "this token was once real" — and DEC-67's revoke-and-keep design means every
// token ever issued is still a row to ask about.
//
// THE WORK IS COUNTED AND NOT TIMED, in the shape internal/auth's own
// countingHasher already uses. A timing assertion on a shared runner is a
// flake; a call count is a fact.
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
	// HEADERS MINUS X-Request-Id, which is per-request by construction.
	if got, want := headersFor(revoked), headersFor(unknown); got != want {
		t.Errorf("revoked headers\n  %s\nunknown headers\n  %s", got, want)
	}

	// AND THE WORK. This is what byte-identical cannot say.
	if revokedWork != unknownWork {
		t.Errorf("a revoked token cost %+v and an unknown one cost %+v.\n"+
			"    Equal bytes and unequal work is still an oracle for 'this token was\n"+
			"    once real': the lookup has to be the ONLY branch.",
			revokedWork, unknownWork)
	}
	// THE POSITIVE CONTROL. Two zeroes are equal too, and a handler that never
	// reached the store at all would satisfy the line above.
	if revokedWork.lookups != 1 {
		t.Errorf("a revoked token cost %d share-link lookups, want 1 — this leg is not "+
			"measuring anything", revokedWork.lookups)
	}
	if revokedWork.reads != 0 || revokedWork.mints != 0 {
		t.Errorf("a token with no live link cost %d log reads and %d mints, want 0 and 0",
			revokedWork.reads, revokedWork.mints)
	}
}

// ------------------------------------------------------------- THE TWO HEADERS

// ASSERTED BY PRESENCE AND NOT BY SAMENESS (PD-09).
//
// v7.0's only header-adjacent leg compared two answers to each other and would
// have passed with the header absent from both. These name the values.
//
// AND THEY ARE ON THE 404 AS WELL. A refusal carries no capability, but a
// policy that applies only on success is a policy whose absence is invisible
// until the one response that needed it — and the middleware is applied from
// the route table, above the handler, precisely so that it cannot be
// conditional on what the handler decided.
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

// ------------------------------------------------------------------ THE TTL

// EVERY MINTED URL USES THE PUBLIC LIFETIME, ASSERTED BY CALL SITE (DEC-84).
//
// R2's TTL leg was DELETED at v7.1 for being unfalsifiable — it compared the
// two configured values to each other, so a handler reaching for the private
// one would have reddened nothing. THE RULING ORDERED A REPLACEMENT and this
// is it: the number is read back off the URL the signer produced, and the
// mutation `media.Public` -> `media.Private` in public_handlers.go turns 900
// into 120.
//
// THE TWIN'S LIFETIMES ARE THE DEPLOYED ONES. media.NewMemory defaults to
// DEC-44's two minutes and DEC-84's fifteen, which are the numbers
// deploy/.env.example carries.
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

// ----------------------------------------------------------- THE THIRD BUCKET

// EXHAUSTING THE PUBLIC READ DOES NOT REFUSE A SIGN-IN (PD-09).
//
// Under the derivation the route table used to make, `GET /l/{token}` would
// have inherited AUTH_RATE_LIMIT_PER_MIN — a CREDENTIAL budget of 10/min —
// FROM THE SAME BUCKET INSTANCE as register and sign-in. Twelve reads of a
// shared trip and nobody can sign in for a minute.
//
// BOTH HALVES ARE ASSERTED. That the public read IS limited, so this is not a
// leg about an unlimited route; and that the credential routes are untouched
// by it.
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

// ------------------------------------------------------------------ THE TWINS

// countingObjects wraps the real media twin and counts read mints.
//
// IT WRAPS RATHER THAN REPLACES, for the reason the harness uses media.Memory
// in the first place: a twin that accepts what MinIO refuses turns a handler
// leg into evidence about nothing. What is added here is a counter, and
// nothing else.
type countingObjects struct {
	media.Store
	mints int
}

func (c *countingObjects) PresignGet(ctx context.Context, key media.Key, aud media.Audience) (string, error) {
	c.mints++
	return c.Store.PresignGet(ctx, key, aud)
}

func (c *countingObjects) reset() { c.mints = 0 }

// fakePublic is logbook.PublicStore over the same document every other twin in
// this package writes, plus the `share_links` rows fakeShare keeps beside it.
//
// IT APPLIES THE THREE ROW RULES, because a twin that published rows the store
// would not would make every leg in this file evidence about nothing. That the
// SQL applies them is internal/postgres's leg and internal/seed's; that a
// document filtered this way carries the allowlist and no more is this file's.
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
			// REGARDLESS OF `revoked`, which is the whole of PD-12: the row
			// comes back and the caller decides, so the lookup is the only
			// branch.
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

	// The trip's cities, in the trip's own order.
	for _, id := range src.Trip.CityIDs {
		for _, city := range f.books.doc.Cities {
			if city.ID == id {
				src.Cities = append(src.Cities, city)
			}
		}
	}
	// RULE 1 and RULE 2 together: a place is published because it has a visit
	// on this trip, and it carries only those visits.
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
	// RULE 3.
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

// ---------------------------------------------------------------- THE HELPERS

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
//
// IT GOES THROUGH THE REAL ROUTES rather than writing to the twin, so what it
// proves includes the wiring: a share minted by `POST /v1/trips/{id}/share` is
// one `GET /l/{token}` can resolve.
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
// document, WITH EVERY FLAG-SENSITIVE FIELD POPULATED — a note, a caption, a
// pin, a photograph's coordinate with its accuracy, a track and a cover.
//
// A LEG THAT SAYS "THIS IS GONE" IS ABOUT A FILTER AND NEVER ABOUT A FIXTURE
// THAT NEVER HAD ONE, which is why this writes directly rather than through
// eight more routes: what it is setting up is the SHAPE, and the routes that
// write each field have their own legs elsewhere in this package.
//
// IT ALSO PLANTS THE TWO ROWS THAT MUST NOT BE PUBLISHED: a wishlist place in
// the same city, and a walk N1's 'Discard' was pressed on.
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
				// ANOTHER TRIP'S VISIT ON THE SAME PLACE — the nested row a
				// place-level filter publishes.
				{ID: "v-other", PlaceID: placeID, TripID: "elsewhere", At: at, Note: &note},
			},
		},
		// A WISHLIST PIN in the same city, never been to.
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
		// N1's 'Discard'.
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

// golden compares against internal/httpapi/testdata and rewrites with -update.
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
