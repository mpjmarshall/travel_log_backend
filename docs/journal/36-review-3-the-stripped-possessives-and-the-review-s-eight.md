## REVIEW-3 — the stripped possessives, and the review's eight were forty

An architecture review listed eight ungrammatical doc comments left behind by
the sweep that stripped decision ids out of comments. **There were forty**, and
the difference is the check rather than the reading.

The review's grep was `\b(is|of|parses|has|does)'s\b` over non-test files. It
is right about the shape and narrow in two directions at once: five words, and
`grep -v _test`. A possessive can hang off any word that cannot own anything,
and a test file's doc comments are read by the same person.

**Rule 4 of `scripts/check-comments.py`** is the widened form: a closed set of
copulas, prepositions, conjunctions and finite verbs, and a `'s` attached to
any of them. Run against the tree at `5b89e94` it reported **40 faults across
34 files** — every one genuine, no false positive among them:

```
cmd/api/main.go:57: "parses's" — a possessive on a word that cannot own
    anything, so a noun was stripped out of this sentence and never put back
internal/logbook/emit.go:6: "is's" …
internal/logbook/validate.go:53: "answers's" …
40 comment fault(s)
```

Three of the forty carried a **second** stripped clause on the same comment,
which no possessive pattern could have found — `internal/media/minio.go`'s
"a host, / Whether to speak TLS", `internal/httpapi/routes_test.go`'s
"at's first request, / That is the argument", and
`internal/seed/photo_writes_test.go`'s "by counting / Than copied from a
report". They were repaired because the fix was already in that line.

**And one false positive was measured and NOT exempted.**
`internal/postgres/schema_test.go` had a parameter named `have`, in the
have/want test idiom, so `have's` was grammatical. The comment was reworded
rather than the word removed from the list: `has's` and `does's` are exactly
the shape the rule exists for, and widening the exemption to protect one
parameter name would have reopened it. The collision is written into the
script's own docstring, where the next person meets it.

**Mutation, restored by file copy and verified with `cmp -s`:** putting
`is's` back into `internal/logbook/emit.go:6` reddens the check naming the
file, the line and the fragment; restored, `0 comment fault(s)`.

### Commands, not numbers

```bash
python3 scripts/check-comments.py                                  # 0 faults
grep -rnE "\b(is|of|parses|has|does)'s\b" --include='*.go' .       # nothing
make check                                                         # exit 0
```

