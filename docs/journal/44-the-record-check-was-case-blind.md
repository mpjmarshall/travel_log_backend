## REVIEW-2d — the record check was green because macOS does not care

The nightly's second run failed at the `record` phase:

```
FAIL a comment names docs/public-envelope.md, which is not in the tree
    internal/postgres/share_read_test.go:2
    internal/httpapi/public_handlers_test.go:27
```

The file is **`docs/PUBLIC-ENVELOPE.md`**. Two comments named it in lower case,
and `scripts/slice-arc.sh record` had passed on the developer's machine every
time it had ever been run, because **`[ -e ]` is case-insensitive on macOS's
default filesystem and case-sensitive on Linux**.

**That is a check that could only fail on a machine nobody ran it on.** The
phase exists to catch exactly this class — VS1-FIXES found a Dockerfile comment
citing two files that had never existed — and it has now been defeated by the
one thing about the host it never asked.

`exists_with_this_case` compares the basename against the directory listing
rather than asking the filesystem whether a path exists. The check is red on
macOS now too, which is the point: an artefact check that passes on the only
machine anybody runs it on is not a check.

**And its first draft was wrong in the way this file keeps recording.** `ls -1`
does not list dotfiles, so `deploy/.env.example` became a false positive
immediately — a second FAIL against correct work, in the first run of a check
written to catch stale records. `ls -1A`. The seventh artefact check to go red
against correct code, and the reason is the same every time: the first draft is
wrong about the artefact rather than about the code.

### Commands, not numbers

```bash
scripts/slice-arc.sh record     # exit 0, and now red on macOS if the case moves
```
