# travellog

The Go + PostgreSQL backend for the Travel Log **logbook** client
(`/Users/mattmarshall/Documents/Flutter/Travel Log` — **READ-ONLY**; nothing in
this repository writes there). One binary, `cmd/api`. Standard library first:
`net/http`'s own `ServeMux`, `encoding/json`, `database/sql`, `log/slog`,
`regexp`, `context`.

**This repository is being built as a VERTICAL SLICE first** (DEC-55, from
agent-graph-spec-V4 §1): one authenticated traveller writing one trip to
Postgres through the real HTTP stack in Docker, reading it back through the one
conditional read, and still having it after the stack restarts. Eight steps,
four tables, five routes. When the slice is green and checked in, the remaining
nineteen steps of the parent plan are **re-planned with the slice as
`entry_state` — not resumed.**

This file is written **step by step, in the same commit as the code** (DEC-23).
A record written while the reasoning is fresh is trustworthy; one reconstructed
at the end is how stale counts and wrong justifications get in. The client's own
CLAUDE.md carries four counts that went wrong from exactly that.

**Standing instruction:** when a claim here is corrected, record **what was
wrong and how it was measured**, not only the corrected fact. The correction is
usually worth more than the fact that replaced it.

---

