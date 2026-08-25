// The two auth routes, over the real mux and the real middleware chain, with a
// fake store. Test-first.
//
// IT RUNS WITHOUT A DATABASE ON PURPOSE. The two legs that matter most here —
// the byte-identical failure and the 429 ceiling — are about what leaves the
// process, and putting them behind a TEST_DATABASE_URL skip is putting them
// where legs go to stop being run.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"travellog/internal/auth"
	"travellog/internal/httpx"
	"travellog/internal/media"
)

// === the fixture ===

type fakeStore struct {
	mu         sync.Mutex
	travellers map[string]stored
	sessions   map[string]auth.Session
	owners     map[string]auth.Traveller
	next       int
	failWith   error
	clock      func() time.Time
}

type stored struct {
	auth.Traveller
	hash string
}

func newStore() *fakeStore {
	return &fakeStore{
		travellers: map[string]stored{},
		sessions:   map[string]auth.Session{},
		owners:     map[string]auth.Traveller{},
	}
}

func (f *fakeStore) CreateTraveller(_ context.Context, email, hash string) (auth.Traveller, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return auth.Traveller{}, f.failWith
	}
	key := strings.ToLower(email)
	if _, held := f.travellers[key]; held {
		return auth.Traveller{}, auth.ErrEmailTaken
	}
	f.next++
	// A UUID AND NOT `traveller-1`, AND IT IS NOT COSMETIC. `travellers.id` is
	// a `uuid` column and internal/media refuses anything else outright —
	// `media.Address` checks the shape because the traveller is a PATH SEGMENT
	// in the bucket, so a value that could carry a `/`, a `.` or a `%` would
	// let one traveller's key reach outside their own prefix. The old value
	// made every media route answer 500 from inside the signer, which is how
	// this was found.
	tr := auth.Traveller{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", f.next), Email: email}
	f.travellers[key] = stored{Traveller: tr, hash: hash}
	return tr, nil
}

func (f *fakeStore) TravellerByEmail(_ context.Context, email string) (auth.Traveller, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return auth.Traveller{}, "", f.failWith
	}
	held, ok := f.travellers[strings.ToLower(email)]
	if !ok {
		return auth.Traveller{}, "", auth.ErrNoTraveller
	}
	return held.Traveller, held.hash, nil
}

func (f *fakeStore) CreateSession(_ context.Context, travellerID string, tokenHash []byte, expiresAt time.Time) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return "", f.failWith
	}
	id := fmt.Sprintf("session-%d", len(f.sessions)+1)
	// LastUsedAt MIRRORS `sessions.last_used_at NOT NULL DEFAULT now()`, and
	// it is not decoration: DEC-100 decides whether to stamp the column by
	// comparing the STORED value against the clock, so a fake whose sessions
	// are born at the zero time reports every one of them as infinitely stale.
	f.sessions[string(tokenHash)] = auth.Session{
		ID: id, TravellerID: travellerID,
		TokenHash:  append([]byte(nil), tokenHash...),
		LastUsedAt: f.now(), ExpiresAt: expiresAt,
	}
	for _, held := range f.travellers {
		if held.ID == travellerID {
			f.owners[id] = held.Traveller
		}
	}
	return id, nil
}

func (f *fakeStore) SessionByTokenHash(_ context.Context, tokenHash []byte) (auth.Session, auth.Traveller, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return auth.Session{}, auth.Traveller{}, f.failWith
	}
	held, ok := f.sessions[string(tokenHash)]
	if !ok {
		return auth.Session{}, auth.Traveller{}, auth.ErrNoSession
	}
	return held, f.owners[held.ID], nil
}

func (f *fakeStore) TouchSession(context.Context, string, string, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failWith
}

// RevokeSession and RevokeEverySession mirror `UPDATE … WHERE revoked_at IS
// NULL`. The fake honours the "already revoked moves nothing" half, because
// that is what the bool and the count are for.
func (f *fakeStore) RevokeSession(_ context.Context, travellerID string, tokenHash []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return false, f.failWith
	}
	held, ok := f.sessions[string(tokenHash)]
	if !ok || held.TravellerID != travellerID || held.RevokedAt != nil {
		return false, nil
	}
	at := f.now()
	held.RevokedAt = &at
	f.sessions[string(tokenHash)] = held
	return true, nil
}

func (f *fakeStore) RevokeEverySession(_ context.Context, travellerID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return 0, f.failWith
	}
	var moved int64
	at := f.now()
	for key, held := range f.sessions {
		if held.TravellerID != travellerID || held.RevokedAt != nil {
			continue
		}
		held.RevokedAt = &at
		f.sessions[key] = held
		moved++
	}
	return moved, nil
}

// TravellerExists is DEC-86's question, and the fake answers it the way the
// table does: any row at all, whatever its address.
//
// THE FAKE HONOURS THE RULE RATHER THAN LEAVING IT TO POSTGRESQL, for the
// reason fakeLogbook honours DEC-89's pointer contract: a fake that says yes
// to everything makes every handler leg green against the contract the service
// was just given. It is also what lets the 409 be asserted with no database,
// which is where R4 measured a 164-leg gap.
func (f *fakeStore) TravellerExists(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return false, f.failWith
	}
	return len(f.travellers) > 0, nil
}

// now is the clock `sessions.last_used_at` defaults to. The harness sets it to
// the same fixed instant the service reads.
func (f *fakeStore) now() time.Time {
	if f.clock == nil {
		return time.Time{}
	}
	return f.clock()
}

const fixedNow = "2027-10-12T09:00:00Z"

// cheapArgon keeps the suite honest about time rather than about cost: the
// shipped parameters are 64 MiB a call and this file makes dozens of them.
var cheapArgon = auth.Argon2id{Params: auth.Params{
	Memory: 8 << 10, Time: 1, Threads: 1, KeyLen: 16, SaltLen: 8,
}}

type harness struct {
	server  *httptest.Server
	store   *fakeStore
	logbook *fakeLogbook
	media   *fakeMedia
	objects *media.Memory
	deps    Deps
	logs    *bytes.Buffer
	client  *http.Client
	addrs   *addressLog
}

// addressLog records the client address of every request that reaches the
// chain.
//
// IT EXISTS SO THAT "two travellers, ONE address" IS MEASURED RATHER THAN
// ASSUMED. That is the premise the whole per-traveller keying claim rests on:
// a limiter keyed on the address, on a constant, or on the path passes every
// leg written from loopback unless something asserts the addresses were the
// same. httptest is expected to give both callers 127.0.0.1, and a leg whose
// premise is an expectation about the test harness is a leg that stops meaning
// what it says the day the harness changes.
type addressLog struct {
	mu   sync.Mutex
	seen map[string]int
}

func (a *addressLog) record(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seen[key]++
}

func (a *addressLog) distinct() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.seen))
	for key := range a.seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func recordAddresses(a *addressLog) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a.record(httpx.ClientKey(r))
			next.ServeHTTP(w, r)
		})
	}
}

type options struct {
	ratePerMin      int
	travellerPerMin int
	maxConcurrent   int
	hasher          auth.Hasher
}

func newHarness(t *testing.T, opt options) *harness {
	t.Helper()
	if opt.ratePerMin == 0 {
		opt.ratePerMin = 1000
	}
	if opt.travellerPerMin == 0 {
		opt.travellerPerMin = 1000
	}
	if opt.maxConcurrent == 0 {
		opt.maxConcurrent = 8
	}
	if opt.hasher == nil {
		opt.hasher = cheapArgon
	}

	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	store := newStore()
	// The same clock the service reads, so a session is born fresh — see
	// fakeStore.TravellerExists's sibling comment on LastUsedAt.
	store.clock = func() time.Time {
		at, _ := time.Parse(time.RFC3339, fixedNow)
		return at
	}

	gate, err := auth.NewGate(opt.maxConcurrent)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	service := &auth.Service{
		Store:  store,
		Hasher: auth.Capped{Hasher: opt.hasher, Gate: gate},
		Now: func() time.Time {
			at, _ := time.Parse(time.RFC3339, fixedNow)
			return at
		},
	}

	books := &fakeLogbook{}
	// THE GEOGRAPHY TWIN IS ONE STRUCT SATISFYING BOTH R6 PORTS, because a
	// place write names a city and the two have to agree about what exists.
	geography := &fakeGeography{books: books}
	// THE MOMENTS TWIN IS ONE STRUCT SATISFYING BOTH R7 PORTS, over the same
	// document — so a photograph written here is one a later re-file can name,
	// and a walk written here is one N1's Discard can flag.
	moments := &fakeMoments{books: books}
	// THE MEDIA TWIN IS THE REAL ONE (media.Memory), NOT A STUB THAT SAYS YES.
	// It enforces the digest, the exact length, the content type, the
	// write-once and a bucket that has to exist — because a twin that accepts
	// what MinIO refuses turns a handler leg into evidence about nothing. The
	// bucket is created here so the ordinary path works; the leg that cares
	// about a bucket that does not exist makes its own.
	objects := media.NewMemory()
	if err := objects.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	rows := newFakeMedia()
	deps := Deps{
		Auth:           service,
		Logbook:        books,
		Share:          &fakeShare{books: books},
		Cities:         geography,
		Places:         geography,
		Photos:         moments,
		Walks:          moments,
		Log:            log,
		AuthLimit:      httpx.NewLimiter(opt.ratePerMin, nil),
		TravellerLimit: httpx.NewLimiter(opt.travellerPerMin, nil),
		Media:          rows,
		Objects:        objects,
		MediaMaxBytes:  testMediaMaxBytes,
		Now: func() time.Time {
			at, _ := time.Parse(time.RFC3339, fixedNow)
			return at
		},
	}
	mux := http.NewServeMux()
	Mount(mux, deps)

	// THE PROTECTED ROUTE IS THE TEST'S, NOT THE PACKAGE'S. VS6 builds the
	// middleware and VS7 builds the first route that wears it, so mounting one
	// in lib/ here would be VS7's work arriving early and unasked for. What
	// this proves is exactly what VS7 will rely on: RequireTraveller resolves
	// the bearer and puts the traveller where a handler reads it.
	mux.Handle("GET /probe", RequireTraveller(service, log)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			tr, ok := auth.TravellerFrom(r.Context())
			if !ok {
				t.Errorf("the protected handler ran with no traveller on the context")
				httpx.WriteError(w, r, httpx.CodeInternal)
				return
			}
			httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"id": tr.ID, "email": tr.Email})
		})))

	addrs := &addressLog{seen: map[string]int{}}
	server := httptest.NewServer(httpx.Chain(mux,
		append(httpx.Base(log, 30*time.Second), recordAddresses(addrs))...))
	t.Cleanup(server.Close)
	return &harness{
		server: server, store: store, logbook: books, deps: deps,
		media: rows, objects: objects,
		logs: logs, client: server.Client(), addrs: addrs,
	}
}

type answer struct {
	status int
	body   []byte
	header http.Header
}

func (h *harness) post(t *testing.T, path, body string) answer {
	t.Helper()
	return h.do(t, http.MethodPost, path, body, "")
}

func (h *harness) put(t *testing.T, path, body, bearer string) answer {
	t.Helper()
	return h.do(t, http.MethodPut, path, body, bearer)
}

func (h *harness) do(t *testing.T, method, path, body, bearer string) answer {
	t.Helper()
	return h.doWithHeaders(t, method, path, body, bearer, nil)
}

func (h *harness) doWithHeaders(t *testing.T, method, path, body, bearer string, headers map[string]string) answer {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	read, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return answer{status: resp.StatusCode, body: read, header: resp.Header.Clone()}
}

func (a answer) decode(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(a.body, &out); err != nil {
		t.Fatalf("the body is not JSON (%d): %q: %v", a.status, a.body, err)
	}
	return out
}

const registered = `{"email":"matt@example.com","passphrase":"a long enough passphrase"}`

// credentialsFor is `registered` for any address, so a leg needing a SECOND
// traveller does not need a second constant.
func credentialsFor(email string) string {
	return fmt.Sprintf(`{"email":%q,"passphrase":"a long enough passphrase"}`, email)
}

// === THE LEG WRITTEN FIRST ===

// DEC-61 and the slice's own verified_by: a wrong passphrase and an unknown
// address must produce an IDENTICAL body and status. The comparison is on the
// BYTES rather than on a decoded map, because `{"code":"unauthenticated"}` and
// `{"code":"unauthenticated","field":""}` decode to the same three fields'
// worth of meaning and are two different answers on the wire.
func TestAWrongPassphraseAndAnUnknownAddressAreByteIdentical(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}

	wrong := h.post(t, "/v1/auth/session",
		`{"email":"matt@example.com","passphrase":"not the passphrase"}`)
	unknown := h.post(t, "/v1/auth/session",
		`{"email":"nobody@example.com","passphrase":"not the passphrase"}`)

	if wrong.status != http.StatusUnauthorized {
		t.Errorf("a wrong passphrase = %d, want 401", wrong.status)
	}
	if unknown.status != wrong.status {
		t.Errorf("the two failures answer %d and %d", wrong.status, unknown.status)
	}
	if !bytes.Equal(wrong.body, unknown.body) {
		t.Errorf("the two failures differ on the wire:\n  wrong passphrase: %q\n  unknown address:  %q\n"+
			"    The difference between them is a list of who has an account here.",
			wrong.body, unknown.body)
	}
	if string(wrong.body) != `{"code":"unauthenticated"}` {
		t.Errorf("the body is %q, want %q", wrong.body, `{"code":"unauthenticated"}`)
	}

	// The headers too: a Content-Length that differs by one is the same oracle
	// wearing a different hat, and so is a header present on one and not the
	// other. X-Request-Id is per-request by design and is the only exclusion.
	for _, a := range []answer{wrong, unknown} {
		a.header.Del(httpx.RequestIDHeader)
		a.header.Del("Date")
	}
	if fmt.Sprint(wrong.header) != fmt.Sprint(unknown.header) {
		t.Errorf("the two failures carry different headers:\n  wrong:   %v\n  unknown: %v",
			wrong.header, unknown.header)
	}
}

// === register ===

func TestRegisterAnswers201WithTheTravellerAndNoTokenAnywhere(t *testing.T) {
	h := newHarness(t, options{})
	got := h.post(t, "/v1/auth/register", registered)

	if got.status != http.StatusCreated {
		t.Fatalf("register = %d %s, want 201", got.status, got.body)
	}
	body := got.decode(t)
	if body["email"] != "matt@example.com" {
		t.Errorf("email = %v", body["email"])
	}
	if id, _ := body["id"].(string); id == "" {
		t.Errorf("no id in %s", got.body)
	}
	name, held := body["name"]
	if !held {
		t.Errorf("`name` is absent from %s; DEC-61 leaves it NULL and the client reads a\n"+
			"    missing name as a log nobody has named yet — absent and null are two\n"+
			"    different statements and only one of them is true here", got.body)
	}
	if name != nil {
		t.Errorf("name = %v, want null", name)
	}
	if _, minted := body["token"]; minted {
		t.Errorf("register minted a token: %s\n"+
			"    DEC-61: not auto-signing-in keeps the sign-in path exercised from the\n"+
			"    FIRST launch rather than making it a second-launch-only branch.", got.body)
	}
	if strings.Contains(string(got.body), "argon2") {
		t.Errorf("the passphrase hash reached the response: %s", got.body)
	}
	if strings.Contains(string(got.body), "a long enough passphrase") {
		t.Errorf("the passphrase reached the response: %s", got.body)
	}
}

// DEC-86, ON THE WIRE: ANY second registration is 409, and the three are BYTE
// IDENTICAL.
//
// It replaces TestRegisteringAnAddressTwiceInAnotherCasingIs409, whose name
// promised the CASING was the reason and which would now pass against a build
// that still handed a stranger an account. The byte comparison is the half
// that matters: the security lens flagged the old 409-on-duplicate as an
// enumeration surface, and what closes it is not the status but the response
// being the same response — a caller cannot tell "that address is taken" from
// "this instance is in use", because there is nothing left to tell them apart
// with.
func TestAnySecondRegistrationIs409AndTheThreeAreIndistinguishable(t *testing.T) {
	answers := map[string]answer{}
	for _, second := range []string{"a@b.com", "A@B.com", "a-total-stranger@example.com"} {
		h := newHarness(t, options{})
		if got := h.post(t, "/v1/auth/register",
			`{"email":"A@B.com","passphrase":"a long enough passphrase"}`); got.status != http.StatusCreated {
			t.Fatalf("the first register = %d %s", got.status, got.body)
		}
		got := h.post(t, "/v1/auth/register",
			fmt.Sprintf(`{"email":%q,"passphrase":"a different long one"}`, second))
		if got.status != http.StatusConflict {
			t.Errorf("registering %q second = %d, want 409 — ruling 3 is single-user, "+
				"and a stranger who registers on a deployed instance gets an "+
				"authenticated account", second, got.status)
		}
		if string(got.body) != `{"code":"conflict"}` {
			t.Errorf("registering %q second: body = %q, want %q", second, got.body, `{"code":"conflict"}`)
		}
		answers[second] = got
	}

	same := answers["a@b.com"]
	for address, got := range answers {
		if !bytes.Equal(got.body, same.body) || got.status != same.status {
			t.Errorf("registering %q answered %d %q, and the same-address case answered %d %q.\n"+
				"    A difference here is an enumeration oracle: it tells a caller whether\n"+
				"    a particular address is the one this instance belongs to.",
				address, got.status, got.body, same.status, same.body)
		}
	}
}

// AND THE FIELD CHECKS STILL COME FIRST ON A CLOSED INSTANCE. A malformed
// address is a 422 naming the field whether or not registration is open;
// answering 409 to it would tell a client to stop trying when what it needs to
// do is fix the body.
func TestAClosedRegistrationStillAnswers422ToABodyItCannotUse(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("the first register = %d %s", got.status, got.body)
	}
	got := h.post(t, "/v1/auth/register",
		`{"email":"not-an-address","passphrase":"a long enough passphrase"}`)
	if got.status != http.StatusUnprocessableEntity {
		t.Errorf("a malformed address on a closed instance = %d %s, want 422", got.status, got.body)
	}
	if field := got.decode(t)["field"]; field != "email" {
		t.Errorf("the 422 names %v, want \"email\"", field)
	}
}

// A SMOKE LEG, AND NAMED AS ONE AFTER A MUTATION SHOWED IT COULD NOT SEE WHAT
// ITS FIRST NAME CLAIMED. It was called
// TestSigningInResolvesTheAddressInAnyCasing, and it cannot: the fake store
// folds case on lookup exactly as travellers_email_lower_key does, so a
// handler that upper-cased the address before the lookup passes it —
// measured, `strings.ToUpper(body.Email)` at the SignIn call site left this
// leg green. DEC-65's casing rule is guarded where the index is, in
// internal/postgres. What this proves is the wiring: two routes, one after the
// other, over the real mux and the real chain.
func TestRegisterThenSignInGoesThroughTheRealChain(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register",
		`{"email":"A@B.com","passphrase":"a long enough passphrase"}`); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	got := h.post(t, "/v1/auth/session",
		`{"email":"A@B.com","passphrase":"a long enough passphrase"}`)
	if got.status != http.StatusCreated {
		t.Errorf("sign in straight after register = %d %s", got.status, got.body)
	}
}

func TestABadFieldIs422AndNamesTheField(t *testing.T) {
	h := newHarness(t, options{})
	for name, c := range map[string]struct{ body, field string }{
		"no at sign":    {`{"email":"matt.example.com","passphrase":"a long enough passphrase"}`, "email"},
		"no email":      {`{"passphrase":"a long enough passphrase"}`, "email"},
		"short":         {`{"email":"matt@example.com","passphrase":"short"}`, "passphrase"},
		"no passphrase": {`{"email":"matt@example.com"}`, "passphrase"},
	} {
		got := h.post(t, "/v1/auth/register", c.body)
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: = %d %s, want 422", name, got.status, got.body)
			continue
		}
		body := got.decode(t)
		if body["code"] != "invalid_field" {
			t.Errorf("%s: code = %v, want invalid_field", name, body["code"])
		}
		if body["field"] != c.field {
			t.Errorf("%s: field = %v, want %q", name, body["field"], c.field)
		}
	}
}

func TestABodyThatIsNotTheJSONThisRouteExpectsIs400(t *testing.T) {
	h := newHarness(t, options{})
	for name, body := range map[string]string{
		"empty":         "",
		"not json":      "hello",
		"an array":      `["matt@example.com"]`,
		"a wrong type":  `{"email":42,"passphrase":"a long enough passphrase"}`,
		"two documents": registered + registered,
	} {
		for _, path := range []string{"/v1/auth/register", "/v1/auth/session"} {
			got := h.post(t, path, body)
			if got.status != http.StatusBadRequest {
				t.Errorf("%s %s: = %d %s, want 400", name, path, got.status, got.body)
			}
			if string(got.body) != `{"code":"invalid_body"}` {
				t.Errorf("%s %s: body = %q", name, path, got.body)
			}
		}
	}
}

func TestAStoreFailureIs500AndSaysNothingElse(t *testing.T) {
	h := newHarness(t, options{})
	h.store.failWith = errors.New("the database named travellog at 127.0.0.1:5434 went away")

	got := h.post(t, "/v1/auth/register", registered)
	if got.status != http.StatusInternalServerError {
		t.Errorf("= %d %s, want 500", got.status, got.body)
	}
	if string(got.body) != `{"code":"internal"}` {
		t.Errorf("body = %q, want %q", got.body, `{"code":"internal"}`)
	}
	if strings.Contains(string(got.body), "127.0.0.1") {
		t.Errorf("the driver error reached the body: %s", got.body)
	}
	if !strings.Contains(h.logs.String(), "went away") {
		t.Errorf("the driver error reached NEITHER the body nor the log, so nothing\n"+
			"    anywhere records why the request failed:\n%s", h.logs.String())
	}
}

// === sign in ===

func TestSignInAnswers201WithATokenAndItsExpiry(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	got := h.post(t, "/v1/auth/session", registered)
	if got.status != http.StatusCreated {
		t.Fatalf("sign in = %d %s, want 201", got.status, got.body)
	}

	body := got.decode(t)
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("no token in %s", got.body)
	}
	if _, err := auth.HashToken(token); err != nil {
		t.Errorf("the token is not the shape auth mints: %v", err)
	}
	expires, _ := body["expiresAt"].(string)
	at, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		t.Fatalf("expiresAt = %q: %v", expires, err)
	}
	pinned, _ := time.Parse(time.RFC3339, fixedNow)
	if !at.Equal(pinned.Add(auth.DefaultSessionTTL)) {
		t.Errorf("expiresAt = %s, want %s", at, pinned.Add(auth.DefaultSessionTTL))
	}
}

// The acceptance check DEC-08 and spec L24 share: the plaintext appears in ONE
// response body, in no log line and in no column. The column half is
// internal/postgres's; the other two are here.
func TestThePlaintextTokenAppearsInOneBodyAndInNoLogLine(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	issued := h.post(t, "/v1/auth/session", registered)
	token, _ := issued.decode(t)["token"].(string)
	if token == "" {
		t.Fatalf("no token in %s", issued.body)
	}

	// Use it, so an access log written for an AUTHENTICATED request is in the
	// buffer too.
	h.do(t, http.MethodGet, "/probe", "", "Bearer "+token)

	if strings.Contains(h.logs.String(), token) {
		t.Errorf("the plaintext token is in the log:\n%s", h.logs.String())
	}
	for _, held := range h.store.sessions {
		if string(held.TokenHash) == token {
			t.Errorf("the plaintext token reached the store")
		}
	}

	again := h.post(t, "/v1/auth/session", registered)
	if second, _ := again.decode(t)["token"].(string); second == token {
		t.Errorf("two sign-ins answered the same token")
	}
}

// === DEC-48 ===

func TestBothAuthRoutesAreRateLimited(t *testing.T) {
	for _, path := range []string{"/v1/auth/register", "/v1/auth/session"} {
		h := newHarness(t, options{ratePerMin: 3})
		var last answer
		for range 4 {
			last = h.post(t, path, registered)
		}
		if last.status != http.StatusTooManyRequests {
			t.Errorf("%s: the 4th request in a minute at a limit of 3 = %d %s, want 429",
				path, last.status, last.body)
		}
		if string(last.body) != `{"code":"rate_limited"}` {
			t.Errorf("%s: body = %q", path, last.body)
		}
	}
}

// DEC-48's own named leg, asserted ON THE RESPONSE and not on a timer: the
// N+1th CONCURRENT login is REFUSED, not made to wait. Queueing converts
// memory exhaustion into timeout exhaustion, which is the same outage wearing
// a different error.
func TestTheNPlusOnethConcurrentLoginIsRefusedWith429(t *testing.T) {
	const n = 2
	inner := &parkingHasher{
		entered: make(chan struct{}, 64),
		release: make(chan struct{}),
		real:    cheapArgon,
	}
	h := newHarness(t, options{maxConcurrent: n, hasher: inner})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	inner.park.Store(true)

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); h.post(t, "/v1/auth/session", registered) }()
	}
	for range n {
		select {
		case <-inner.entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only some of the %d logins reached the hasher", n)
		}
	}

	refused := make(chan answer, 1)
	go func() { refused <- h.post(t, "/v1/auth/session", registered) }()
	select {
	case got := <-refused:
		if got.status != http.StatusTooManyRequests {
			t.Errorf("the %dth concurrent login = %d %s, want 429", n+1, got.status, got.body)
		}
		if string(got.body) != `{"code":"rate_limited"}` {
			t.Errorf("body = %q, want %q", got.body, `{"code":"rate_limited"}`)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("the %dth concurrent login is still waiting after 5s — it was QUEUED.\n"+
			"    DEC-48 rejects queueing by name.", n+1)
	}

	close(inner.release)
	wg.Wait()
}

// parkingHasher holds every call open once `park` is set, so a test can have N
// logins genuinely in flight at once. Two real Argon2 calls on a fast machine
// may simply not overlap, which would make the ceiling leg pass for no reason.
type parkingHasher struct {
	real    auth.Hasher
	entered chan struct{}
	release chan struct{}
	park    atomicBool
}

func (p *parkingHasher) hold() {
	if !p.park.Load() {
		return
	}
	p.entered <- struct{}{}
	<-p.release
}

func (p *parkingHasher) Hash(s string) (string, error) { p.hold(); return p.real.Hash(s) }
func (p *parkingHasher) Verify(e, s string) (bool, error) {
	p.hold()
	return p.real.Verify(e, s)
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) Store(v bool) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicBool) Load() bool   { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// === the routes themselves ===

// THE TWO CREDENTIAL PATHS TAKE THE VERBS THE TABLE GIVES THEM AND NOTHING
// ELSE.
//
// `DELETE /v1/auth/session` IS NOW A REAL ROUTE, so it comes off the refused
// list — and taking it off is not a weakening, because the leg's claim was
// never "no other verb exists" but "no other verb is SERVED". A route that
// arrives is a decision somebody made; a route that arrives and quietly
// satisfies a leg written to refuse it is the shape this project keeps
// finding. The whole table's coverage is asserted in routes_test.go, which
// iterates rather than lists.
func TestTheCredentialRoutesTakeTheVerbsTheTableGivesThem(t *testing.T) {
	h := newHarness(t, options{})
	refused := map[string][]string{
		"/v1/auth/register": {http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch},
		// DELETE is served here since R5. It is authenticated, so an
		// unauthenticated DELETE is a 401 rather than a 405 — asserted below
		// so the difference is stated rather than left as a gap in this list.
		"/v1/auth/session": {http.MethodGet, http.MethodPut, http.MethodPatch},
	}
	for path, methods := range refused {
		for _, method := range methods {
			got := h.do(t, method, path, registered, "")
			if got.status != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", method, path, got.status)
			}
		}
	}

	if got := h.do(t, http.MethodDelete, "/v1/auth/session", "", ""); got.status != http.StatusUnauthorized {
		t.Errorf("DELETE /v1/auth/session with no credential = %d, want 401 — it is the "+
			"revocation surface, and revoking without a credential would let anybody "+
			"sign anybody out", got.status)
	}
}

// === the middleware ===

// A SURVIVOR IS ARGUED HERE RATHER THAN LEFT UNMENTIONED. Deleting
// RequireTraveller's `if !held` branch — reading the token with `_` and
// letting Authenticate answer — leaves this leg GREEN, measured. The two
// paths reach the same 401 by different roads: one refuses to READ a
// credential, the other refuses to ACCEPT one, and an absent header arrives
// at the second as the empty string, which HashToken rejects on length.
//
// The branch is kept anyway, and the reason is that the redundancy is between
// FILES. Without it, "no Authorization header at all" is answered correctly
// only because auth/token.go refuses a zero-length token — a property of
// another package, guarded by another leg, that nothing here would notice the
// loss of. One line, and it does not depend on somebody else's invariant.
func TestAnAuthenticatedRouteRefusesEveryShapeOfMissingCredential(t *testing.T) {
	h := newHarness(t, options{})
	for name, bearer := range map[string]string{
		"absent":         "",
		"another scheme": "Basic abc",
		"malformed":      "Bearer !!!!",
		"unheld":         "Bearer " + strings.Repeat("A", 43),
	} {
		got := h.do(t, http.MethodGet, "/probe", "", bearer)
		if got.status != http.StatusUnauthorized {
			t.Errorf("%s: = %d %s, want 401", name, got.status, got.body)
		}
		if string(got.body) != `{"code":"unauthenticated"}` {
			t.Errorf("%s: body = %q", name, got.body)
		}
	}
}

func TestAnAuthenticatedRouteSeesTheTravellerTheTokenNames(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	issued := h.post(t, "/v1/auth/session", registered)
	token, _ := issued.decode(t)["token"].(string)

	got := h.do(t, http.MethodGet, "/probe", "", "Bearer "+token)
	if got.status != http.StatusOK {
		t.Fatalf("the protected route = %d %s, want 200", got.status, got.body)
	}
	if got.decode(t)["email"] != "matt@example.com" {
		t.Errorf("the protected route answered %s", got.body)
	}
}

func TestAStoreFailureUnderTheMiddlewareIs500AndNot401(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	issued := h.post(t, "/v1/auth/session", registered)
	token, _ := issued.decode(t)["token"].(string)

	h.store.mu.Lock()
	h.store.failWith = errors.New("the database went away")
	h.store.mu.Unlock()

	got := h.do(t, http.MethodGet, "/probe", "", "Bearer "+token)
	if got.status != http.StatusInternalServerError {
		t.Errorf("= %d %s, want 500.\n"+
			"    A database that has gone must not be reported as a credential that is\n"+
			"    not live: the client's answer to 401 is to sign in again, which cannot\n"+
			"    work and loses the session it was holding.", got.status, got.body)
	}
}

// DEC-48 is a ruling, and a ruling like this regresses silently: an optional
// dependency left unset reads as working software and removes the only bound
// on unauthenticated Argon2 work. Mount refuses instead.
func TestMountRefusesToRunWithoutARateLimiter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("Mount accepted a nil limiter, so the auth routes would serve\n" +
				"    unlimited Argon2 work and every DEC-48 leg above would still pass")
		}
	}()
	Mount(http.NewServeMux(), Deps{Auth: &auth.Service{}, Log: slog.Default()})
}
