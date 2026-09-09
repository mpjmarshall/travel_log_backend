# The sweeps

Thirteen test files walk the AST or the import graph to enforce an architecture
rule. They are the strongest thing in this repository and the mechanism most
likely to go red against correct work — **seven of them have**, and every time
the first draft was wrong about the artefact rather than about the code.

This index exists so nobody has to read all thirteen to learn what is enforced.

**A new sweep must name, in its own doc comment, the shipped defect it would
have caught, and this index is updated in the same commit.** A sweep that
enforces a preference rather than a defect is the thing that makes the next
person delete the one that matters. `scripts/check-record.py` fails `make check`
if the file count and the row count disagree, in either direction.

**Exemption lists are asserted by EQUALITY, not as a ceiling.** An entry that
matches nothing is itself a fault, because a stale exemption is a hole with a
comment over it. That is the rule for every list in the table below.

Re-derive the set rather than trusting this list:

```bash
grep -rl 'go/ast\|go/parser' --include='*_test.go' . | sort
```

| file | the rule | the defect class it exists for | exemptions |
|---|---|---|---|
| [`cmd/api/imports_test.go`](../cmd/api/imports_test.go) | pgx is imported **only blank** and only where it is the driver; nothing under `cmd/api` reaches `internal/seed`, transitively | `grep -rn 'jackc/pgx'` returning one line is equally satisfied by a *named* import followed by a direct call, which is what "solely as a blank import driver" forbids. A grep sees the line and not the underscore. | `pgxImporters` |
| [`internal/admin/imports_sweep_test.go`](../internal/admin/imports_sweep_test.go) | the panel imports no persistence adapter | a template rendering a database row makes a second adapter impossible and puts SQL vocabulary on a page. | `persistencePackages` |
| [`internal/auth/store_sweep_test.go`](../internal/auth/store_sweep_test.go) | every method on the `Store` port is reached through it | a method nothing calls through the port costs every implementation and every twin a method for nothing. Reading found one dead member; running found two. | — |
| [`internal/auth/token_test.go`](../internal/auth/token_test.go) | the token file uses the packages the spec names, by name | **not a sweep file — one AST leg inside an ordinary test file.** It is in the derived set because it imports `go/ast`, and saying so is cheaper than an exemption. | — |
| [`internal/config/sweep_test.go`](../internal/config/sweep_test.go) | `internal/config` is the only package that reads the environment | a grep for `os.Getenv` matches its own source, matches comments, and **cannot see `os.LookupEnv`** — a one-word bypass reading the same environment. | `environmentExemptions` |
| [`internal/httpapi/dance_sweep_test.go`](../internal/httpapi/dance_sweep_test.go) | the ETag, the credential preamble and the path-id reconciliation each exist in **one** place | a rule written by hand in nine handlers is a rule nine handlers have to remember, and the ninth forgets. | `credentialExemptions` |
| [`internal/httpapi/deps_sweep_test.go`](../internal/httpapi/deps_sweep_test.go) | every field on `Deps` is nil-guarded by `Mount`; no handler takes the whole bag | a field added to `Deps` and left unguarded is a nil that reaches a handler and 500s on the first request. Its own first draft counted a *use* as a guard and hid two gaps, one of them `*auth.Service`. | `unguardedDeps`, `depsHolders` |
| [`internal/httpapi/emit_sweep_test.go`](../internal/httpapi/emit_sweep_test.go) | every domain entity on the wire goes through an `logbook.Emit*` | a handler answering a bare entity writes `"visits": null`, and the client reads that field as a non-nullable list. It shipped once, and the GET was correct the whole time. | `bareBodies` |
| [`internal/httpapi/logpath_test.go`](../internal/httpapi/logpath_test.go) | no log site writes anything share-token-shaped; every site naming a path asks `LoggedPath` for it | the capability lives in the URL, so the one line written while somebody is enumerating tokens is the line recording the capability being enumerated. The rate limiter writes **two** log lines, and a fix applied to one leaves the other. | — |
| [`internal/httpx/sweep_test.go`](../internal/httpx/sweep_test.go) | exactly two files import `encoding/json` and exactly two functions use it; the twelve-word block and the runtime map are one set; every wire word is a named constant | typing the parameter does **not** close the vocabulary — an untyped string constant converts implicitly, so `WriteError(w, r, "banana")` compiles. And the acceptance grep this replaced went red against a *comment*. | `jsonImporters`, `wireCodeExemptions` |
| [`internal/media/ban_test.go`](../internal/media/ban_test.go) | neither presign call that signs only `host` is used | two of minio-go's three presign methods sign the host and nothing else. Measured: with only `host` signed, an attacker who omits the digest header stores arbitrary bytes at an address claiming to be their hash, and gets **200**. | `banned` |
| [`internal/postgres/refusal_sweep_test.go`](../internal/postgres/refusal_sweep_test.go) | only named functions author a field refusal here | 37 of the field vocabulary's 96 constructions were in a package the domain does not import, so `visitAt`, `coordinates` and `centre` were wire-visible field names `internal/logbook` had never heard of. | `refusalsAuthoredHere` |
| [`internal/postgres/tx_sweep_test.go`](../internal/postgres/tx_sweep_test.go) | nothing outside the allowlist opens a transaction; the session writes take the locking helper and not the bumping one; register takes neither | a write that opens its own transaction skips the per-traveller advisory lock and the version bump, so the ETag stops describing the document. Its allowlist once carried an entry for a file that would never exist. | `transactionAllowlist` |

## What no sweep guards

- **That the sweeps themselves are run.** They are ordinary `go test` legs, so
  `make check` runs them — but a sweep deleted outright takes its rule with it,
  and only the row count below notices.
- **Whether a rule is still the right rule.** Every one of these encodes a
  decision; the decisions are in `docs/journal/`.
