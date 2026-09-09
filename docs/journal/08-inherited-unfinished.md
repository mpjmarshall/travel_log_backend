## Inherited unfinished

Recorded as deferrals rather than allowed to read as simplifications.

- **Caddy, and `X-Forwarded-For` with it.** Two services, not three. The rate
  limiter **keys on `RemoteAddr`** as of VS3 — **correct for a direct
  connection and wrong the moment a proxy appears**, at which point every
  request arrives from the proxy's address and the limiter is one bucket for
  the whole internet. The limiter-behind-proxy leg (two different
  `X-Forwarded-For` values, one `RemoteAddr`, separate buckets) belongs to the
  step that adds Caddy, and does not exist yet. `httpx.ClientKey` is the one
  function that changes.
- **`internal/httpx` has no caller in `lib` code.** VS3 built the chain; VS4
  turned out not to be where a mux goes through it — VS4 is the runner and the
  schema, and the routes are VS6/VS7. Two consequences, both small and both real:
  `Timeout(d)` wants a duration `internal/config` does not read yet (there is
  no `REQUEST_TIMEOUT`), and `/healthz` still writes its body with
  `fmt.Fprintf(w, "{%q:%q}\n", …)` — the tree's only hand-rolled JSON writer.
  Converting it is one line and was **tried and reverted**: it reddens
  `TestProbeAgreesWithTheRealMux`, which asserts the body byte-for-byte
  *including its trailing newline*, and `httpx.WriteJSON` deliberately emits
  none. The newline question is settled once, for every route, by the step that
  wires the mux.
- **`make slice`** — **done at VS8**, and off this list with `make migrate`,
  which VS4 implemented. The entry is kept rather than deleted because what it
  said about a target that fails loudly is now a leg: see the `record` phase.
- **The DEC-27 floor attribution** — see above; VS1 has no dependencies and
  cannot answer it.
- **The third budget: a general per-address ceiling over the whole API.**
  Added at VS8-SEC, which built the second one. There are now two — per address
  for the credential routes, per traveller for the authenticated ones — and
  neither covers a route with **no identity at all**. The public share read (R8)
  is the first such route, and it is also what turns `Route`'s middleware
  derivation into the sixth field that step declined. The same budget is what
  would bound the session lookup an authenticated request pays for before the
  per-traveller ceiling can see it. One step, three things, and none of them is
  urgent until the share read lands.

---

