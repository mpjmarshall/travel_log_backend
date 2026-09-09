## The gate

```bash
make check
```

`go build ./...` → `go vet ./...` → `gofmt -l .` (must print **nothing**) →
`go test ./...`. **There is no CI. This is the only gate, and it is manual.**

**Why `go vet` is mandatory rather than a nicety.** The module directive is
written literally as `go 1.25.0` (DEC-27), and vet is one of the two commands
that enforce the language floor. It is in the chain because a build can be
served from cache while vet re-resolves the module graph; keeping both means the
floor is checked by two independent paths rather than one.

**Why `gofmt -l .` is inspected rather than chained.** `gofmt -l` exits **0**
whether or not it lists a single file *it can parse*. A bare `gofmt -l . && …`
is therefore a check that cannot fail — the class the parent plan calls out at
DEC-28. The Makefile captures its output and fails on non-empty.

**CORRECTION (VS1-FIXES): that premise was half the story, and the missing half
was a hole in the only gate this project has.** On a **syntax error** gofmt
exits **2** and writes to **stderr**. The recipe captured stdout only and
tested `[ -n "$out" ]`, so `out` was empty, the test was false, the recipe line
succeeded, and **`make check` exited 0 with a `.go` file in the tree that gofmt
could not parse.** Measured at `ee543b9` on a file copy, with the malformed
file at `.tools/broken.go` — a hidden directory, which `./...` does not match
and which `internal/config`'s AST sweep skips at `sweep_test.go:67`, so no
other step in the gate saw it either:

```
$ gofmt -l . ; echo "gofmt exit=$?"
.tools/broken.go:2:12: expected ')', found '{'
gofmt exit=2
$ make check          # VS2's Makefile, at ee543b9
go build ./...
go vet ./...
.tools/broken.go:2:12: expected ')', found '{'
go test ./...
ok  	travellog/cmd/api	0.303s
ok  	travellog/internal/config	0.460s
MAKE EXIT=0
```

The recipe now captures `$?` as well as the output and checks the status first;
the same tree gives `MAKE EXIT=2`. **And the lesson is worth more than the
fix.** VS1 proved this step with **one** mutation — a badly formatted but
*parseable* file — and recorded the class as closed. That mutation still
reddens (re-run as a control at `ee543b9`: `MAKE EXIT=2`, "gofmt -l reported
unformatted files"). **A guard proven once against one mutation is proven
against that mutation, not against its class.** The step has three recorded
legs now — unparseable → 2, misformatted → 1, clean → 0 — under VS1-FIXES with
their output.

**Every target does what it says now.** This paragraph used to read
"`make migrate` and `make slice` fail non-zero today", and both halves have
been overtaken — VS4 implemented `migrate`, VS8 implemented `slice`. The
sentence it existed for is the one to keep: **a target that exits 0 having
done nothing is indistinguishable from one that succeeded**, and that is how a
missing step gets counted as a passing one. It is now a leg rather than a
paragraph — the arc's `record` phase runs `make slice SLICE=<stub>` against a
stub exiting 0 and a stub exiting 3, and asserts make answers 0 and 2. (The
exit-2 number itself was wrong here in two places until VS1-FIXES, which is
why it is derived by running the target rather than written down.)

**`make check` leaves `./api` behind, and that is `go build ./...`, not a bug in
the Makefile.** With a single main package in the pattern, `go build ./...`
writes the executable into the **current directory**. Measured at VS1 — `git
add -A` staged a 9 MB binary. The command is kept literal, because the gate is
specified as those four commands in that order, so `/api` is git-ignored with
the reason written beside it. Do not "tidy" that line away without changing the
gate first.

---

