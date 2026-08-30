# Admin panel — implementation plan

**Goal:** a web-facing administration panel at `/admin`, served by the API
binary, that monitors travellers and performs a fixed set of curated actions
against the database.

**Architecture:** a new `internal/admin` package holding handlers, an
in-memory session store, and `html/template` views embedded with `embed.FS`.
It mounts onto the existing `http.ServeMux` in `cmd/api`, behind its own
password, and mounts nothing at all when that password is unset. Reads go
through a new `internal/postgres/admin_store.go`; every write reuses an
existing store method so invariants that already hold keep holding.

**Tech:** Go 1.25, `html/template`, htmx (vendored), `database/sql`, the
existing `internal/httpx` middleware.

**Decisions:** settled by interview on 30 August 2026 and recorded in full in
§1 below. Where this plan and that table disagree, the table wins.

---

## 1. The decisions

Twenty-nine, counted rather than carried.

### Shape

| # | Decision |
|---|---|
| 1 | Admin authentication is a **separate password from config**, unrelated to traveller auth. A traveller can never escalate into it. |
| 2 | The panel is **web-facing**. The security boundary is the password, not the network. |
| 3 | It performs **curated actions on real entities**, not generic row editing. |
| 4 | It mounts on the **same binary and the same port**, under `/admin`. |
| 5 | Views are `html/template`; **htmx is vendored**, never a CDN. |
| 6 | It **matches the app's dark design system**. |
| 7 | The colour tokens are **hand-transcribed** into CSS custom properties, with `lib/src/theme/app_colors.dart` named in a comment. A generator in the gate is impossible: the Go repository's CI has no copy of the Flutter one. |
| 8 | **Playfair Display for headings, system sans for body.** 300KB rather than 540KB, and Playfair carries most of the identity. |

### Authentication

| # | Decision |
|---|---|
| 9 | The credential is `ADMIN_PASSWORD`, compared in constant time. |
| 10 | **Minimum 12 characters.** Set but shorter refuses at boot, naming the variable. |
| 11 | **Unset mounts nothing.** No `/admin` routes exist and the API boots exactly as it does today, so no existing stack breaks. |
| 12 | **No default anywhere**, including development. `compose` passes it through and `.env.example` documents it empty. |
| 13 | Sessions are **in-memory with a 12-hour idle TTL**. A restart signs the operator out. This breaks under more than one replica, and that is accepted. |
| 14 | The cookie is `HttpOnly`, `SameSite=Strict`, `Secure`. **`Secure` is dropped only under `DEVELOPMENT=1`** — the same seam the log mail sender already uses. |
| 15 | Brute force is met with **the rate limiter and a lockout**: 10 failures, then 15 minutes. |
| 16 | CSRF is **`SameSite=Strict` and a per-session token**, carried by htmx through `hx-headers`. |
| 17 | **No impersonation, ever.** The panel administers accounts and cannot read a log as its owner. `CreateSession` is never called from `internal/admin`. |

### Actions

| # | Decision |
|---|---|
| 18 | Travellers: list, rename, delete. |
| 19 | Rename goes through `LogbookStore.SetTravellerName`, so `logbook_version` bumps and clients re-read. Raw SQL here would leave every client showing the old name until something else moved the version. |
| 20 | Delete is a **hard delete behind a typed-name confirmation** — the operator types the traveller's email exactly. It cascades to eleven tables and there is no undo but the backup. |
| 21 | After the rows go, **each object is removed from the bucket, best effort, and every failure is logged by object id.** Rows first: a failed bucket call then leaves a recoverable orphan rather than a live photo pointing at missing bytes. |
| 22 | Invites: list, mint, revoke. **Revoke deletes the row** — an unused invite is a live credential. |
| 23 | Sessions appear **both globally and per traveller**, and can be revoked. |
| 24 | The log browse is **metadata and counts only**. The panel never renders a photograph and never mints a presigned GET. |
| 25 | The travellers list has **search and paging** from the start. |
| 26 | The dashboard shows **counts, storage and recent sign-ins**. |
| 27 | Every mutating action writes an **`slog` line**: actor, action, target, result. No audit table. |

### Build

| # | Decision |
|---|---|
| 28 | Tests cover **handlers including the auth and CSRF refusals, stores against the test database, and the execution of every template**. |
| 29 | Documentation is **this plan and a new section in `BEFORE-A-PUBLIC-DEPLOY.md`**, which is the record of what changes when local-only stops being true. |

---

## 2. What this changes about the deployment

`docs/BEFORE-A-PUBLIC-DEPLOY.md` opens by saying this repository's target is
local only, that everything is loopback-bound, and that every password in it is
guessable on purpose. The panel is the first thing here that contradicts that,
in four ways, and §10 of that document now records them:

- **The first credential that is not a traveller's.** One password administers
  every account.
- **The first cookie, and the first browser-facing surface.** Nothing in this
  repository has ever set `Set-Cookie` or a `Content-Security-Policy`; the API
  is JSON only.
- **The first thing that deletes from the bucket.** See §3.
- **`ADMIN_PASSWORD` is the first variable with no safe default.** Decision 12
  is deliberately unlike the rest of the stack.

## 3. A pre-existing hole this work exposes

**Nothing in this codebase deletes bucket objects.** `grep` for a delete on
`internal/media` returns nothing, and `media.Store` has four methods:
`EnsureBucket`, `PresignPut`, `PresignGet`, `Stat`.

So today, deleting a traveller cascades their `media_objects` rows away and
**leaves the bytes in storage for ever** — unreferenced, unreachable, and still
present after the account is "deleted". Decision 21 closes this for the panel's
own delete path by adding a fifth method. It does **not** sweep orphans that
already exist; that is a separate job and is noted in §14.

One mercy, checked rather than assumed: `media_objects` cascades from
`travellers` and is keyed per traveller, so removing one person's objects can
never affect another's. Content addressing does not share rows across accounts.

---

## 4. File structure

```
internal/admin/
  admin.go          Deps, Mount, and the decision that unset means unmounted
  session.go        the in-memory store, its TTL, and the CSRF token
  login.go          GET and POST /admin/login, the lockout, the cookie
  middleware.go     requireSession, requireCSRF, securityHeaders
  handlers.go       dashboard, travellers, traveller, invites, sessions
  actions.go        rename, delete, mint, revoke — every mutating route
  render.go         the template set, its funcs, and the render helper
  templates/
    layout.gohtml   the shell: head, nav, the hx-headers CSRF attribute
    login.gohtml
    dashboard.gohtml
    travellers.gohtml   full page
    _travellers.gohtml  the table body alone, for htmx search and paging
    traveller.gohtml
    invites.gohtml
    _invites.gohtml
    sessions.gohtml
    _confirm.gohtml     the typed-name delete dialog
  static/
    admin.css       the 34 transcribed tokens, then the panel's own rules
    htmx.min.js     vendored, version recorded in §13
    PlayfairDisplay-Variable.ttf
    OFL-PlayfairDisplay.txt

internal/postgres/
  admin_store.go        every read the panel makes, and DeleteTraveller

internal/media/
  store.go              gains Delete(ctx, Key) error
  s3.go                 implements it with minio RemoveObject
```

`cmd/api/main.go` gains one call to `admin.Mount`, and
`internal/config/config.go` gains one field and one loader helper.

---

## 5. Global constraints

Every task inherits these.

- **The comment rules are checked.** `scripts/check-comments.py` runs in
  `make check`: no comment inside a `func`, `const` or `type` body; at most two
  consecutive comment lines; under 20% of a file for files of 40 lines or more.
- **`make check` must pass**, which needs `TEST_DATABASE_URL` exported; the
  `make test-db` target prints the line but does not export it.
- **Never call `CreateSession` from `internal/admin`.** Decision 17.
- **Never log the password, the session token or the CSRF token.**
- **Never mint a presigned GET from the panel.** Decision 24.
- Work on a branch, run the gate, merge with `--no-ff`. Never commit to `main`.

---

## 6. Task 1 — Config takes ADMIN_PASSWORD

**Files:** modify `internal/config/config.go`, `internal/config/config_test.go`,
`deploy/docker-compose.yml`, `deploy/.env.example`.

**Produces:** `Config.AdminPassword string`, empty when unset.

The existing loader helpers all treat a variable as required. This one is
optional but constrained, which is a new shape:

- [ ] **Step 1: the failing test.**

```go
func TestAdminPasswordIsOptionalButNotShort(t *testing.T) {
	setEnv(t, complete())
	cfg, err := config.Load()
	if err != nil || cfg.AdminPassword != "" {
		t.Fatalf("unset ADMIN_PASSWORD must load cleanly and be empty, got %q, %v",
			cfg.AdminPassword, err)
	}

	setEnv(t, with("ADMIN_PASSWORD", "short"))
	if _, err := config.Load(); err == nil ||
		!strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Errorf("a password under the floor must refuse and name itself, got %v", err)
	}
}
```

- [ ] **Step 2: run it.** `go test ./internal/config/ -run TestAdminPassword`.
  Expected: FAIL, `cfg.AdminPassword` undefined.

- [ ] **Step 3: implement.** Add to `Config`, and a helper beside the others:

```go
func (l *loader) optional(name string, minLength int) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return ""
	}
	if len(v) < minLength {
		l.add(name, fmt.Sprintf("%d characters, and the floor is %d", len(v), minLength))
	}
	return v
}
```

Wire it as `AdminPassword: l.optional("ADMIN_PASSWORD", MinAdminPassword)` with
`const MinAdminPassword = 12`.

- [ ] **Step 3a: clear it in `setEnv`.** `setEnv` makes `allVars` the whole of
  what `Load` can see, and `ADMIN_PASSWORD` does not belong in `allVars` — that
  list is the variables an empty environment must *name in its error*, and this
  one is optional. So it must be cleared separately, or a developer who exports
  `ADMIN_PASSWORD` in their shell gets different results from CI. This is the
  same two-jobs-one-list confusion that hid the missing `DEVELOPMENT` entry
  from `TestComposeSetsEveryVariableTheConfigPackageReads`.

- [ ] **Step 4: compose and the template.** `ADMIN_PASSWORD: ${ADMIN_PASSWORD:-}`
  in the api service's environment, and `ADMIN_PASSWORD=` in `.env.example`
  under a two-line comment saying what it enables and that there is no default.

- [ ] **Step 5: run the whole config package.**
  `TestComposeSetsEveryVariableLoadReads` derives its list from `Load`'s body,
  so it fails until compose sets the variable. That is the guard working.

- [ ] **Step 6: commit.**

---

## 7. Task 2 — The session store

**Files:** create `internal/admin/session.go`, `internal/admin/session_test.go`.

**Produces:** `Sessions` with `New(now) (id, csrf string)`,
`Get(id, now) (csrf string, ok bool)`, `Drop(id)`, and
`const IdleTTL = 12 * time.Hour`.

**Consumes:** nothing.

- [ ] **Step 1: the failing tests.** Three, and the third is the one worth
  having:

```go
func TestASessionExpiresAfterTheIdleWindow(t *testing.T) {
	s := admin.NewSessions()
	at := time.Unix(1_700_000_000, 0)
	id, _ := s.New(at)

	if _, ok := s.Get(id, at.Add(admin.IdleTTL-time.Second)); !ok {
		t.Fatal("a session inside the window must still be live")
	}
	if _, ok := s.Get(id, at.Add(admin.IdleTTL+time.Second)); ok {
		t.Error("a session past the window must be gone")
	}
}

func TestReadingASessionExtendsIt(t *testing.T) { /* Get slides the deadline */ }

func TestTwoSessionsNeverShareATokenOrACSRF(t *testing.T) {
	s := admin.NewSessions()
	at := time.Unix(1_700_000_000, 0)
	id1, c1 := s.New(at)
	id2, c2 := s.New(at)
	if id1 == id2 || c1 == c2 || id1 == c1 {
		t.Error("ids and CSRF tokens must be independently random")
	}
}
```

- [ ] **Step 2: run them.** Expected: FAIL, package does not exist.

- [ ] **Step 3: implement.** A `sync.Mutex`, a `map[string]entry{csrf string;
  deadline time.Time}`, and 32 bytes from `crypto/rand` hex-encoded for each of
  the two tokens. `Get` slides the deadline on a hit and deletes on a miss.
  The clock is a parameter, never `time.Now()` inside.

- [ ] **Step 4: run them.** Expected: PASS.
- [ ] **Step 5: commit.**

---

## 8. Task 3 — Login, the lockout, and the cookie

**Files:** create `internal/admin/login.go`, `internal/admin/login_test.go`.

**Consumes:** `Sessions` from Task 2, `Config.AdminPassword` from Task 1.

**Produces:** `login(deps) http.HandlerFunc`, `logout(deps) http.HandlerFunc`,
`const CookieName = "admin_session"`, `MaxFailures = 10`,
`LockFor = 15 * time.Minute`.

- [ ] **Step 1: the failing tests.**

```go
func TestTheWrongPasswordDoesNotSetACookie(t *testing.T)
func TestTheRightPasswordSetsAStrictSecureHttpOnlyCookie(t *testing.T)
func TestTenFailuresLockTheLoginForFifteenMinutes(t *testing.T)
func TestTheLockoutLiftsOnceTheWindowPasses(t *testing.T)
func TestSecureIsDroppedOnlyUnderDevelopment(t *testing.T)
```

The cookie one asserts all four attributes explicitly, because a cookie missing
`Secure` on a web-facing panel travels in clear text:

```go
c := readSetCookie(t, rec)
if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
	t.Errorf("cookie is %+v, want HttpOnly Secure SameSite=Strict", c)
}
```

- [ ] **Step 2: run.** Expected: FAIL.
- [ ] **Step 3: implement.** `subtle.ConstantTimeCompare` on the password.
  Failures counted in memory against `httpx.ClientKey(r)`; on reaching
  `MaxFailures` the handler answers the login page with a locked message until
  the window passes, and a correct password during a lock is still refused.
  A success clears the counter and calls `Sessions.New`.
- [ ] **Step 4: run.** Expected: PASS.
- [ ] **Step 5: commit.**

---

## 9. Task 4 — Middleware, and mounting nothing

**Files:** create `internal/admin/middleware.go`, `internal/admin/admin.go`,
and their tests. Modify `cmd/api/main.go`.

**Produces:** `admin.Mount(mux *http.ServeMux, deps Deps)`, which returns
immediately when `deps.Password == ""`.

- [ ] **Step 1: the failing tests.** The first is the most important in the
  whole plan:

```go
func TestNoPasswordMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	admin.Mount(mux, admin.Deps{Password: ""})

	for _, path := range []string{"/admin", "/admin/login", "/admin/travellers"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d with no password set, want 404: a panel "+
				"nobody configured must not exist", path, rec.Code)
		}
	}
}

func TestEveryPageRefusesWithoutASession(t *testing.T)
func TestAMutatingRouteRefusesWithoutTheCSRFToken(t *testing.T)
func TestEveryAdminResponseCarriesItsSecurityHeaders(t *testing.T)
```

- [ ] **Step 2: run.** Expected: FAIL.
- [ ] **Step 3: implement.** `requireSession` reads the cookie, asks `Sessions`,
  and redirects to the login page on a miss. `requireCSRF` wraps every non-GET
  and compares the `X-CSRF-Token` header against the session's token in
  constant time. `securityHeaders` sets
  `Content-Security-Policy: default-src 'self'`, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer` and `Cache-Control: no-store`.
- [ ] **Step 4: run.** Expected: PASS.
- [ ] **Step 5: wire `cmd/api`.** One call beside the healthz registration,
  taking the password from config.
- [ ] **Step 6: commit.**

---

## 10. Task 5 — Templates, tokens and the vendored assets

**Files:** create `internal/admin/render.go`, `templates/layout.gohtml`,
`static/admin.css`, and add the vendored htmx and font files.

**Produces:** `newRenderer() (*renderer, error)` and
`(*renderer).page(w, name string, data any)`.

- [ ] **Step 1: the failing test.** This is decision 28's third leg, and it is
  what makes a template typo fail the gate instead of the page:

```go
func TestEveryTemplateParsesAndExecutes(t *testing.T) {
	r, err := admin.NewRenderer()
	if err != nil {
		t.Fatalf("parsing the template set: %v", err)
	}
	for _, name := range r.Names() {
		if err := r.Execute(io.Discard, name, admin.SampleData(name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
```

- [ ] **Step 2: run.** Expected: FAIL, no renderer.
- [ ] **Step 3: implement.** `//go:embed templates/*.gohtml static/*` and
  `template.Must`-free parsing that returns its error. `layout.gohtml` puts the
  CSRF token on the body as `hx-headers='{"X-CSRF-Token": "..."}'` so every
  htmx request inherits it.
- [x] **Step 4: transcribe the tokens.** **Thirty-four, not the 23 this plan
  first said** — that figure came from a grep counting only direct `Color(0x…)`
  constructions. Re-derived: 22 primitives, 12 semantic aliases built on them,
  and one `LinearGradient` which CSS expresses differently and is left out. A
  leg names all 34, so dropping one reddens.
- [ ] **Step 5: vendor the assets.** `htmx.min.js`, `PlayfairDisplay-Variable.ttf`
  and `OFL-PlayfairDisplay.txt`, the last of which must ship for the licence to
  be satisfied. Serve them under `/admin/static/`.
- [ ] **Step 6: run.** Expected: PASS.
- [ ] **Step 7: commit.**

---

## 11. Tasks 6 to 11 — The pages

Each follows one rhythm: a store method with a test against the test database,
a handler with an `httptest` test, a template, and a line in the template
render test's sample data. Each is its own commit.

- [ ] **Task 6 — Dashboard.** `AdminStore.Overview` returns traveller, trip,
  photo, place, live-session and unused-invite counts, bucket bytes from
  `sum(media_objects.byte_size)`, and the ten most recent sessions by
  `created_at`. One page, no actions.

- [ ] **Task 7 — Travellers list.** `AdminStore.Travellers(ctx, query string,
  limit, offset int)` with `lower(email) LIKE` and a total for the pager.
  `_travellers.gohtml` is swapped by htmx on search and on page change, so the
  full page and the fragment share one template.

- [ ] **Task 8 — Traveller detail.** `AdminStore.Traveller(ctx, id)` returning
  the row, per-entity counts, storage used, and that traveller's sessions.
  Metadata only — no photograph, no presigned URL. Decision 24.

- [ ] **Task 9 — Rename.** `POST /admin/travellers/{id}/name`, calling
  `LogbookStore.SetTravellerName` and nothing else, so `logbook_version` bumps.
  The test asserts the version moved, which is the whole point of decision 19:

```go
before := versionOf(t, db, id)
// ... post the new name ...
if versionOf(t, db, id) <= before {
	t.Error("a rename that does not move logbook_version leaves every client " +
		"showing the old name until something else happens to move it")
}
```

- [ ] **Task 10 — Invites.** List with mint date, note and claimed-by; mint
  through `AuthStore.MintInvite`; revoke by deleting the row. The plaintext is
  shown once on the page that mints it and is never stored or logged.

- [ ] **Task 11 — Sessions.** A global list joined to travellers, the same rows
  on the traveller page, and revoke through the existing store methods.

---

## 12. Task 12 — Delete a traveller

**Files:** modify `internal/media/store.go` and its S3 implementation; create
`AdminStore.DeleteTraveller`; add the confirm template and the action.

This is the only irreversible thing the panel does, so it is its own task.

- [ ] **Step 1: the failing tests.**

```go
func TestDeleteRefusesUnlessTheTypedEmailMatchesExactly(t *testing.T)
func TestDeleteRemovesTheTravellerAndEveryCascadedRow(t *testing.T)
func TestDeleteAsksTheBucketToRemoveEveryObjectItOwned(t *testing.T)
func TestABucketFailureStillLeavesTheTravellerDeleted(t *testing.T)
func TestABucketFailureIsLoggedWithTheObjectId(t *testing.T)
```

The fourth and fifth are decision 21's ordering, stated as tests: the rows go
first, so a storage failure leaves a recoverable orphan and never a live
photograph pointing at bytes that are gone.

- [ ] **Step 2: run.** Expected: FAIL.
- [ ] **Step 3: add `Delete` to `media.Store`** and implement it with minio's
  `RemoveObject`. Every existing fake in the test suite needs the method; that
  is the cost of widening the interface and it is small.
- [ ] **Step 4: implement the action.** Read the object ids first, inside the
  transaction that deletes; delete the traveller; then loop the ids calling
  `Delete`, collecting failures into one `slog.Error` naming each id.
- [ ] **Step 5: the confirm page.** The traveller's email typed exactly, with
  the count of what will go beside it, in the shape of the client's D3 sheet.
  The safe action is the prominent one and the destructive one is not.
- [ ] **Step 6: run.** Expected: PASS.
- [ ] **Step 7: commit.**

---

## 13. Task 13 — Verify it against a running stack

Not a test — the third evidence tier, a human at a browser.

- [ ] `travellog-fresh` on `:8095` with `ADMIN_PASSWORD` set, thirteen
  travellers already in it including `ada@example.com` and `grace@example.com`.
- [ ] The panel is unreachable with the variable unset, and the API still boots.
- [ ] A wrong password ten times locks the login; the right one during the lock
  is still refused; it lifts.
- [ ] Search finds a traveller; paging works; a rename shows in the client
  after its next read, which is decision 19 observed rather than asserted.
- [ ] Mint an invite, register with it, watch it become claimed.
- [ ] Delete a traveller created for the purpose, and confirm with `mc ls` or
  the MinIO console that their objects left the bucket.

Record the htmx version vendored, and the exact commit, in
`docs/EVIDENCE.md` alongside the other tier-three runs.

---

## 14. Task 14 — Documentation

- [x] **`BEFORE-A-PUBLIC-DEPLOY.md` §10 is written**, ahead of the code and
  marked as such, covering the four firsts, the shared port, the three things
  the stack does not do for you (TLS, `RemoteAddr` behind a proxy, and recovery
  from a lockout that only a restart clears), and the orphan measurement: 4
  objects and 5,175,532 bytes on the live stack on 30 August 2026, with no code
  path that could ever have removed one.

- [ ] **Re-read §10 when the code lands** and drop its "nothing here is built
  yet" clause. A section written ahead of the work is right until the work
  exists, and then it is stale.

---

## 15. Self-review

Checked against §1 before this plan was handed over.

- Every one of the twenty-nine decisions appears in a task or in §5's
  constraints. Decisions 17 and 24 are prohibitions, so they live in §5 and are
  asserted only by their absence; that is weaker than a test and is said here
  rather than left to look guarded.
- No step says "add validation" or "handle errors" without showing what.
- The names used late are defined early: `Sessions` in Task 2 is what Task 3
  consumes, `Mount` and `Deps` in Task 4 are what `cmd/api` calls, `Delete` on
  `media.Store` is added in Task 12 and nowhere before it.
- **One thing this plan cannot promise.** The panel is the first browser
  surface in this repository, so nothing in `make check` has ever rendered
  HTML, set a cookie, or evaluated a CSP. Task 5's render pass is real
  coverage; a Content-Security-Policy that is present but wrong will still pass
  every test in this plan. That is tier three, and Task 13 is where it is
  caught.
