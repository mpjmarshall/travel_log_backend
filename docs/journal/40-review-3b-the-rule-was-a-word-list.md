## REVIEW-3b — rule 4 was a word list, and the shape is a suffix

REVIEW-3 widened the review's five-word grep to a closed set of copulas,
prepositions, conjunctions and finite verbs, found forty faults and called the
class closed. **It was not.** A word list catches the words on it. `brings's`
turned up two commits later, in a file being edited for another reason, and it
is the same wreckage on a verb nobody thought to list.

**The shape is a SUFFIX, not a vocabulary.** Any word ending in `s` followed by
`'s` — `brings's`, `renders's`, `wraps's`, `keeps's`, `honours's`, `guards's`,
`builds's`, `bounds's` — and it subsumes `is's`, `was's` and `has's` for free.
Measured across every `.go` file: **nine occurrences, eight of them wreckage.**

The function-word list stays beside it, because `of's`, `and's`, `at's` and
`into's` end in no particular letter and no suffix rule reaches them. Two
shapes, one message.

### THE EXEMPTION IS ASSERTED BY EQUALITY, WHICH IS THE POINT

The suffix shape has one real exception in this tree: `harness's`, a noun that
genuinely owns something. `OWNS_LEGITIMATELY` names it — and a name that
matches **nothing** in the tree is itself a fault:

```
scripts/check-comments.py: OWNS_LEGITIMATELY names 'harness' and no comment in
the tree writes harness's — an exemption nothing uses is a hole with a comment
over it
1 comment fault(s)
```

Reddened by rewriting the one comment that uses it, restored by file copy and
verified with `cmp -s`. That is the same rule this repository applies to every
allowlist it has: `jsonImporters`, `wireCodeExemptions`, `refusalsAuthoredHere`.
The first pass reached for a reword instead, on the `have`/`want` collision, and
that was the right call for a *parameter name* and the wrong general answer.

### A SECOND DEFECT, FOUND ONLY BY READING THE SEVEN

`internal/seed/load_test.go` carried a doc comment describing
`documentDifferences` sitting above `alignByID` — a different function, 27 lines
above the one it names, which had no comment at all. The sweep detached it. No
possessive rule could have caught that, and nothing else would have: a doc
comment on the wrong declaration compiles, vets and formats. Both functions have
their own comment now.

### What is still not caught, and cannot be

Two shapes survive both rules and are in `TODO.md`:

- **`sense's`** at `internal/auth/service.go:16` — "untuned in the same sense's
  Argon2 parameters are". `sense` is a noun that can own; only a grammar checker
  distinguishes this from correct prose.
- **`TokenBytes is the 32.`** at `internal/auth/token.go:13` — a noun phrase
  removed from the middle of a sentence, leaving one that parses as English and
  says nothing.

Naming them is the honest end of this: rule 4 catches a shape, not a class.

### Commands, not numbers

```bash
python3 scripts/check-comments.py                          # 0 faults
grep -rnE "//.*\b[A-Za-z]+s's\b" --include='*.go' .        # 1, the exemption
```
