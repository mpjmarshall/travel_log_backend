## The `go 1.25.0` literal — and what VS1 actually measured

The directive must read **`go 1.25.0`**, not `go 1.25`. That is DEC-27, and
VS1 re-measured it rather than carrying it. **The measurement corrects the
plan's attribution in one place and sharpens it in another.**

Measured 22 August 2026, Go 1.26.5 darwin/arm64:

```
# (a) this module, at VS1, with ZERO external dependencies
$ go mod edit -go=1.25 && go build ./... ; echo $?   -> 0
$ go vet ./...                          ; echo $?   -> 0

# (b) a throwaway module importing golang.org/x/crypto/argon2
$ go mod tidy            # RAISES the directive from `go 1.25` to `go 1.25.0`
$ go mod edit -go=1.25   # put it back, i.e. pin it against tidy's advice
$ go build -o /dev/null ./... ; echo $?  -> 1
  go: updates to go.mod needed; to update it:
  	go mod tidy
$ go vet ./...                ; echo $?  -> 1
  go: updates to go.mod needed; to update it:
  	go mod tidy
$ go mod edit -go=1.25.0 && go build … && go vet …   -> 0, 0
```

Three things follow, and the first is a correction:

1. **At VS1 the literal is not yet forced by anything.** The slice has no
   external modules until VS2, and with none, `go 1.25` builds and vets
   cleanly. So VS1's acceptance check `grep '^go ' go.mod` is a **pure
   artefact check** — it can only fail if somebody edits the file, never
   because the code is wrong. It is recorded as such rather than presented as
   evidence about the build. The check acquires teeth at VS2, when pgx and
   x/crypto arrive.
2. **The failure mode is more specific than "1.25 fails".** It is
   `golang.org/x/crypto v0.55.0` and `golang.org/x/sys v0.47.0` each declaring
   `go 1.25.0`, against which the directive comparison treats `1.25` as
   strictly lower. The error is not a compile error — it is
   `go: updates to go.mod needed`, refusing to proceed.
3. **`go mod tidy` will fix it for you, which is the trap.** Tidy silently
   raises `go 1.25` to `go 1.25.0`. The failure only appears if somebody pins
   `1.25` back afterwards. So the thing to guard is not "run tidy" — it is
   "do not re-edit the directive down after tidy has settled it".

`expected_to_contradict` in the slice plan asks whether the floor is forced by
x/crypto alone once minio-go is out of scope. **VS1 cannot answer that** — it
has no dependencies at all. The question stands open for VS2/VS6, where
`grep '^go ' go.mod` after `go mod tidy` is a real check.

---

