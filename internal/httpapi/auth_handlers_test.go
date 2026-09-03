// The two auth routes, over the real mux and the real middleware chain, with
// a fake store.
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
	"travellog/internal/mail"
	"travellog/internal/media"
)

const fixedNow = "2027-10-12T09:00:00Z"

// cheapArgon keeps the suite honest about time rather than about cost: the
// shipped parameters are 64 MiB a call and this file makes dozens of them.

type harness struct {
	server  *httptest.Server
	store   *auth.Memory
	logbook *fakeLogbook
	media   *fakeMedia
	objects *media.Memory
	public  *fakePublic
	deps    Deps

	bearerToken string
	logs        *bytes.Buffer
	posted      *sentMail
	client      *http.Client
	addrs       *addressLog
}

// addressLog records the client address of every request that reaches the
// chain.
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
	publicPerMin    int
	maxConcurrent   int
	mailer          mail.Sender
	geocoder        Geocoder
}

func newHarness(t *testing.T, opt options) *harness {
	t.Helper()
	if opt.ratePerMin == 0 {
		opt.ratePerMin = 1000
	}
	if opt.travellerPerMin == 0 {
		opt.travellerPerMin = 1000
	}
	if opt.publicPerMin == 0 {
		opt.publicPerMin = 1000
	}
	if opt.maxConcurrent == 0 {
		opt.maxConcurrent = 8
	}

	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	store := newStore()
	store.SetClock(func() time.Time {
		at, _ := time.Parse(time.RFC3339, fixedNow)
		return at
	})

	service := &auth.Service{
		Store: store,
		Now: func() time.Time {
			at, _ := time.Parse(time.RFC3339, fixedNow)
			return at
		},
	}

	books := &fakeLogbook{}
	geography := &fakeGeography{books: books}
	moments := &fakeMoments{books: books}
	objects := media.NewMemory()
	if err := objects.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	rows := newFakeMedia()
	shared := &fakePublic{books: books}
	counted := &countingObjects{Store: objects}
	posted := &sentMail{}
	if opt.mailer == nil {
		opt.mailer = posted
	}
	if opt.geocoder == nil {
		opt.geocoder = &fakeGeocoder{}
	}

	deps := Deps{
		Auth:           service,
		Mail:           opt.mailer,
		Logbook:        books,
		Share:          &fakeShare{books: books},
		Cities:         geography,
		Places:         geography,
		Photos:         moments,
		Walks:          moments,
		Public:         shared,
		Geocode:        opt.geocoder,
		Log:            log,
		AuthLimit:      httpx.NewLimiter(opt.ratePerMin, nil),
		TravellerLimit: httpx.NewLimiter(opt.travellerPerMin, nil),
		PublicLimit:    httpx.NewLimiter(opt.publicPerMin, nil),
		Media:          rows,
		Objects:        counted,
		MediaMaxBytes:  testMediaMaxBytes,
		Now: func() time.Time {
			at, _ := time.Parse(time.RFC3339, fixedNow)
			return at
		},
	}
	mux := http.NewServeMux()
	Mount(mux, deps)

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
		media: rows, objects: objects, public: shared,
		logs: logs, client: server.Client(), addrs: addrs, posted: posted,
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

const testInvite = "TESTINVITE"

// newStore is the twin with this package's standing invite already accepted,
// because every leg here registers somebody before it can assert anything.
func newStore() *auth.Memory {
	store := auth.NewMemory()
	store.AcceptInvite(testInvite)
	return store
}

const registered = `{"email":"matt@example.com","passphrase":"a long enough passphrase","invite":"` + testInvite + `"}`

// credentialsFor is `registered` for any address, so a leg needing a second
// traveller does not need a second constant.
func credentialsFor(email string) string {
	return fmt.Sprintf(`{"email":%q,"passphrase":"a long enough passphrase","invite":%q}`, email, testInvite)
}

// withoutAnInvite is credentialsFor with the guard left off, for the legs that
// are about the guard.
func withoutAnInvite(email string) string {
	return fmt.Sprintf(`{"email":%q,"passphrase":"a long enough passphrase"}`, email)
}

// The slice's own verified_by: a wrong passphrase and an unknown address
// must produce an identical body and status.
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

	for _, a := range []answer{wrong, unknown} {
		a.header.Del(httpx.RequestIDHeader)
		a.header.Del("Date")
	}
	if fmt.Sprint(wrong.header) != fmt.Sprint(unknown.header) {
		t.Errorf("the two failures carry different headers:\n  wrong:   %v\n  unknown: %v",
			wrong.header, unknown.header)
	}
}

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

// A second registration is refused by the invite now, not by the count, and
// the three answers stay byte identical.
func TestASecondRegistrationNeedsAnInviteOfItsOwn(t *testing.T) {
	answers := map[string]answer{}
	for _, second := range []string{"a@b.com", "A@B.com", "a-total-stranger@example.com"} {
		h := newHarness(t, options{})
		if got := h.post(t, "/v1/auth/register",
			`{"email":"A@B.com","passphrase":"a long enough passphrase","invite":"`+testInvite+`"}`); got.status != http.StatusCreated {
			t.Fatalf("the first register = %d %s", got.status, got.body)
		}
		got := h.post(t, "/v1/auth/register",
			fmt.Sprintf(`{"email":%q,"passphrase":"a different long one"}`, second))
		if got.status != http.StatusUnprocessableEntity {
			t.Errorf("registering %q with no invite = %d, want 422 — the route is "+
				"open to anybody without one", second, got.status)
		}
		answers[second] = got
	}

	same := answers["a@b.com"]
	for address, got := range answers {
		if string(got.body) != string(same.body) {
			t.Errorf("%q answered %q and a@b.com answered %q, so the reply says "+
				"which addresses are taken", address, got.body, same.body)
		}
	}
}

// / A second traveller with a fresh invite is admitted, which is the whole
// / point of replacing the one-traveller rule.
func TestASecondTravellerWithAFreshInviteIsAdmitted(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", credentialsFor("first@example.com")); got.status != http.StatusCreated {
		t.Fatalf("first = %d %s", got.status, got.body)
	}
	if got := h.post(t, "/v1/auth/register", credentialsFor("second@example.com")); got.status != http.StatusCreated {
		t.Fatalf("a second traveller with an invite = %d %s", got.status, got.body)
	}
}

// / The same address twice is still a conflict, and that is a different
// / refusal from a missing invite.
func TestTheSameAddressTwiceIsStillAConflict(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", credentialsFor("matt@example.com")); got.status != http.StatusCreated {
		t.Fatalf("first = %d %s", got.status, got.body)
	}
	got := h.post(t, "/v1/auth/register", credentialsFor("MATT@example.com"))
	if got.status != http.StatusConflict {
		t.Fatalf("the same address in another casing = %d, want 409: %s", got.status, got.body)
	}
}

func TestAClosedRegistrationStillAnswers422ToABodyItCannotUse(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("the first register = %d %s", got.status, got.body)
	}
	got := h.post(t, "/v1/auth/register",
		`{"email":"not-an-address","passphrase":"a long enough passphrase","invite":"`+testInvite+`"}`)
	if got.status != http.StatusUnprocessableEntity {
		t.Errorf("a malformed address on a closed instance = %d %s, want 422", got.status, got.body)
	}
	if field := got.decode(t)["field"]; field != "email" {
		t.Errorf("the 422 names %v, want \"email\"", field)
	}
}

// A smoke leg, and named as one after A mutation showed it could not see what
// its first name claimed.
func TestRegisterThenSignInGoesThroughTheRealChain(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register",
		`{"email":"A@B.com","passphrase":"a long enough passphrase","invite":"`+testInvite+`"}`); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	got := h.signIn(t, "A@B.com")
	if got.status != http.StatusCreated {
		t.Errorf("sign in straight after register = %d %s", got.status, got.body)
	}
}

func TestABadFieldIs422AndNamesTheField(t *testing.T) {
	h := newHarness(t, options{})
	for name, c := range map[string]struct{ body, field string }{
		"no at sign": {`{"email":"matt.example.com","passphrase":"a long enough passphrase"}`, "email"},
		"no email":   {`{"passphrase":"a long enough passphrase"}`, "email"},
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
	h.store.FailWith(errors.New("the database named travellog at 127.0.0.1:5434 went away"))

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

func TestSignInAnswers201WithATokenAndItsExpiry(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	got := h.signIn(t, "matt@example.com")
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

// The acceptance check and share: the plaintext appears in one response body,
// in no log line and in no column.
func TestThePlaintextTokenAppearsInOneBodyAndInNoLogLine(t *testing.T) {
	h := newHarness(t, options{})
	if got := h.post(t, "/v1/auth/register", registered); got.status != http.StatusCreated {
		t.Fatalf("register = %d %s", got.status, got.body)
	}
	issued := h.signIn(t, "matt@example.com")
	token, _ := issued.decode(t)["token"].(string)
	if token == "" {
		t.Fatalf("no token in %s", issued.body)
	}

	h.do(t, http.MethodGet, "/probe", "", "Bearer "+token)

	if strings.Contains(h.logs.String(), token) {
		t.Errorf("the plaintext token is in the log:\n%s", h.logs.String())
	}
	for _, hash := range h.store.StoredTokenHashes() {
		if string(hash) == token {
			t.Errorf("the plaintext token reached the store")
		}
	}

	again := h.signIn(t, "matt@example.com")
	if second, _ := again.decode(t)["token"].(string); second == token {
		t.Errorf("two sign-ins answered the same token")
	}
}

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

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) Store(v bool) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicBool) Load() bool   { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// The two credential paths take the verbs the table gives them and nothing
// else.
func TestTheCredentialRoutesTakeTheVerbsTheTableGivesThem(t *testing.T) {
	h := newHarness(t, options{})
	refused := map[string][]string{
		"/v1/auth/register": {http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch},
		"/v1/auth/session":  {http.MethodGet, http.MethodPut, http.MethodPatch},
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

// A survivor is argued here rather than left unmentioned.
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
	issued := h.signIn(t, "matt@example.com")
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
	issued := h.signIn(t, "matt@example.com")
	token, _ := issued.decode(t)["token"].(string)

	h.store.FailWith(errors.New("the database went away"))

	got := h.do(t, http.MethodGet, "/probe", "", "Bearer "+token)
	if got.status != http.StatusInternalServerError {
		t.Errorf("= %d %s, want 500.\n"+
			"    A database that has gone must not be reported as a credential that is\n"+
			"    not live: the client's answer to 401 is to sign in again, which cannot\n"+
			"    work and loses the session it was holding.", got.status, got.body)
	}
}

func (h *harness) registerTraveller(t *testing.T, email, passphrase string) {
	t.Helper()
	body := `{"email":"` + email + `","passphrase":"` + passphrase + `","invite":"` + testInvite + `"}`
	if got := h.post(t, "/v1/auth/register", body); got.status != http.StatusCreated {
		t.Fatalf("registering %s: %d %s", email, got.status, got.body)
	}
}

// signIn does the code dance and answers the session route's own reply, so a
// leg about sign-in still asserts on a real response.
func (h *harness) signIn(t *testing.T, email string) answer {
	t.Helper()
	h.store.ClearCode(strings.ToLower(email))
	before := h.posted.count()
	if got := h.post(t, "/v1/auth/code", `{"email":"`+email+`"}`); got.status != http.StatusAccepted {
		t.Fatalf("asking for a code for %s = %d %s", email, got.status, got.body)
	}
	waitFor(t, func() bool { return h.posted.count() > before })
	return h.post(t, "/v1/auth/session",
		`{"email":"`+email+`","code":"`+h.posted.codeFor(t, email)+`"}`)
}
