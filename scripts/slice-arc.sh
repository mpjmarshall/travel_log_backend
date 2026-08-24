#!/usr/bin/env bash
#
# The slice, end to end, against the live stack — and the four standing legs
# earlier steps could not run from inside `go test`.
#
# WHY THIS IS A SCRIPT AND NOT A GO TEST. Three of its five phases invoke
# `make check` or `make test-db`, and a test that invokes the gate from inside
# the gate is circular; the fourth needs two compose projects and a cold
# initdb. `make check` stays four commands and stays fast (measured 4.4s at
# d7a6da9); everything here is opt-in through `make slice`.
#
# ASSERT, DO NOT JUST RUN. A curl that 500s and a curl that 201s both exit 0.
# Every request below has its status checked and its body parsed, because the
# whole reason this file exists is that VS7's suite was green while the write
# path answered "cityIds":null.
#
# THE TWO COMPOSE COMMANDS THAT LOOK ALIKE DO DIFFERENT JOBS:
#   `down -v`  destroys the named volume. It runs ONCE, at the top, so the arc
#              starts from nothing and the 201s below are real creations.
#   `down`     keeps it. It runs at the restart leg, and reading the trip back
#              after it is the ONLY proof `pgdata` works. Swap these two and
#              the restart leg passes while proving the opposite.
#
# Phases run cheapest-first so a stale record fails in a second rather than
# after a two-minute build:
#
#   record       artefact tier — paths named in comments, Makefile wiring
#   gate         `make check` against a file gofmt cannot parse
#   arc          the eleven-step arc, from cold
#   testdb       `make test-db` derives its URL from the running container
#   healthcheck  docker's verdict never disagrees with a real TCP probe
#
# usage: scripts/slice-arc.sh [phase ...]     (default: all five, in order)

set -Eeuo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose -f "$REPO/deploy/docker-compose.yml")

# The email is fixed rather than generated because the arc owns the volume: it
# destroys it at the top, so a fixed address is created exactly once per run and
# every psql assertion below can name it.
ARC_EMAIL="arc@travellog.test"
ARC_PASS="correct-horse-battery-staple"
ARC_TRIP="kyoto"
JSON_CT="Content-Type: application/json"

# The trip body is a single-quoted literal because it carries no variable, and
# it carries shareCoordinates:true on purpose — see A8.
TRIP_BODY='{"name":"Kyoto in May","start":"2027-05-12T00:00:00.000Z","end":"2027-05-19T00:00:00.000Z","shareCoordinates":true}'

WORK="$(mktemp -d "${TMPDIR:-/tmp}/travellog-slice.XXXXXX")"
FAILED=0
CLEANUP=()

trap 'on_exit' EXIT

on_exit() {
	local status=$?
	local target
	for target in "${CLEANUP[@]:-}"; do
		[ -n "$target" ] || continue
		eval "$target" >/dev/null 2>&1 || true
	done
	rm -rf "$WORK"
	if [ "$status" -ne 0 ]; then
		printf '\n\033[31mslice-arc: FAILED\033[0m (exit %s)\n' "$status" >&2
	fi
	exit "$status"
}

phase()  { printf '\n\033[1m=== %s\033[0m\n' "$*"; }
step()   { printf '\n--- %s\n' "$*"; }
ok()     { printf '     ok  %s\n' "$*"; }
fail()   { printf '\033[31m    FAIL %s\033[0m\n' "$*" >&2; exit 1; }

# assert_eq is the whole difference between running the arc and proving it.
assert_eq() {
	local want="$1" got="$2" what="$3"
	if [ "$want" = "$got" ]; then
		ok "$what = $got"
	else
		fail "$what = ${got:-<empty>}, want $want"
	fi
}

assert_contains() {
	local haystack="$1" needle="$2" what="$3"
	case "$haystack" in
	*"$needle"*) ok "$what contains $needle" ;;
	*) fail "$what does not contain $needle: ${haystack:-<empty>}" ;;
	esac
}

# req writes the body to $WORK/body and the headers to $WORK/head and echoes
# the status. --fail is deliberately NOT passed: a 4xx is an answer this script
# asserts on, not an error it aborts at.
#
# THE rm IS THE LEG, NOT HOUSEKEEPING. `curl -o` does not create or truncate the
# file when the response has no body — measured here: the 304 leg read 333 bytes
# and they were the PREVIOUS request's document, so "the 304 answers an empty
# body" passed as "the 304 answers the whole log". An absence assertion is the
# easiest kind to write so that it cannot fail.
req() {
	rm -f "$WORK/body" "$WORK/head"
	curl -sS -o "$WORK/body" -D "$WORK/head" -w '%{http_code}' "$@"
}

# body_bytes counts a file curl never created as zero, which is the answer it
# gives for a bodiless response.
body_bytes() {
	[ -e "$WORK/body" ] || { printf 0; return; }
	wc -c <"$WORK/body" | tr -d ' '
}

header() {
	# HTTP header names are case-insensitive and Go writes them canonicalised;
	# match case-insensitively so the assertion is about the value.
	tr -d '\r' <"$WORK/head" | awk -v name="$(printf '%s' "$1" | tr 'A-Z' 'a-z')" '
		BEGIN { FS = ": " }
		{ key = tolower($1) }
		key == name { sub(/^[^:]*: /, ""); print; exit }'
}

body()   { cat "$WORK/body"; }
jqbody() { jq -r "$@" <"$WORK/body"; }

need() {
	command -v "$1" >/dev/null 2>&1 || fail "$1 is not on PATH, and this script cannot assert without it"
}

########################################################################
# PHASE: record
#
# Artefact tier, and labelled as such: these can only fail when the RECORD is
# wrong, never because the code is. They are here because VS1-FIXES found a
# Dockerfile comment citing two files that had never existed, and a stale
# record is the cheapest defect in this repository to catch and the most
# expensive to inherit.
########################################################################

# comment_lines is every COMMENT line in the tree's own source. Code lines are
# excluded on purpose: an import path or a //go:embed pattern is already checked
# by the compiler, so including them would add noise the check cannot act on.
comment_lines() {
	grep -rhE '^[[:space:]]*(//|#|--)' \
		--include='*.go' --include='*.sql' --include='*.yml' \
		"$REPO/cmd" "$REPO/internal" "$REPO/migrations" "$REPO/deploy" 2>/dev/null
	grep -hE '^[[:space:]]*#' "$REPO/Makefile" "$REPO/Dockerfile" "$REPO/deploy/.env.example" 2>/dev/null
}

# Repo-relative paths a comment may name that are NOT in the tree. Each needs a
# reason, and "it is named in a sentence saying it does not exist" is one — the
# check would otherwise be unimplementable against a record that documents its
# own corrections.
path_exemption() {
	case "$1" in
	internal/rest | internal/rest/auth_handlers.go)
		# DEC-74 renamed away from this. tx_sweep_test.go names it to say so.
		return 0 ;;
	internal/store | internal/store/testdb | internal/store/testdb.go)
		# VS5's predicted package name; it landed as internal/postgres/testdb.
		# The prediction is quoted in sweep_test.go's own correction.
		return 0 ;;
	deploy/.env)
		# Git-ignored, and the root .dockerignore exists to keep it out of the
		# build context. A comment naming it is naming a file that must NOT be
		# in the tree.
		return 0 ;;
	internal/httpapi/middleware.go)
		# Named by internal/auth/bearer.go for a whole VS as if it existed; the
		# comment now names it only to say it never did. Found by this check.
		return 0 ;;
	docs/DIVERGENCES.md)
		# VS1-FIXES finding 6: named by a comment as if it existed. The comment
		# now names it only to say it never did.
		return 0 ;;
	esac
	return 1
}

phase_record() {
	phase "record — every path a comment names, and the Makefile's own wiring"

	step "R1: repo-relative paths named in comments exist"
	local missing=0 candidate
	# Comments only, and code lines are excluded on purpose: an import path or a
	# //go:embed pattern is checked by the compiler already, and a glob is not a
	# path. Trailing sentence punctuation is stripped — "internal/postgres." is
	# a sentence ending, not a directory.
	for candidate in $(comment_lines | grep -oE '(cmd|internal|deploy|migrations|docs|scripts|test)/[A-Za-z0-9_./-]+' |
		sed 's/[.,;:)`]*$//' | sort -u); do
		case "$candidate" in
		*'...'* | *'*'*) continue ;;
		esac
		if [ -e "$REPO/$candidate" ]; then continue; fi
		if path_exemption "$candidate"; then
			ok "$candidate — exempt, with a reason in path_exemption"
			continue
		fi
		printf '\033[31m    FAIL a comment names %s, which is not in the tree\033[0m\n' "$candidate" >&2
		grep -rn --include='*.go' --include='*.sql' --include='*.yml' -F "$candidate" \
			"$REPO/cmd" "$REPO/internal" "$REPO/migrations" "$REPO/deploy" "$REPO/Makefile" 2>/dev/null | head -3 >&2 || true
		missing=$((missing + 1))
	done
	[ "$missing" -eq 0 ] || fail "$missing path(s) named in a comment do not exist"
	ok "every path named in a comment exists or is exempt with a reason"

	step "R2: every documented target is a target, and every target is documented"
	local documented targets
	documented="$(grep -oE '^## [a-z-]+' "$REPO/Makefile" | sed 's/^## //' | sort -u)"
	targets="$(grep -oE '^\.PHONY: .*' "$REPO/Makefile" | sed 's/^\.PHONY: //' | tr ' ' '\n' | sort -u)"
	assert_eq "$(printf '%s' "$targets")" "$(printf '%s' "$documented")" "the ## doc lines and .PHONY"

	step "R3: make slice runs the script, and propagates its exit code"
	# THE CLASS THIS GUARDS IS "a target that exits 0 having done nothing",
	# which VS1-FIXES finding 7 is the record half of. It is checked without
	# recursion by overriding SLICE with a stub, so `make slice` is exercised
	# for its WIRING rather than re-running the arc.
	printf '#!/bin/sh\nexit 0\n' >"$WORK/slice-true"
	printf '#!/bin/sh\nexit 3\n' >"$WORK/slice-false"
	chmod +x "$WORK/slice-true" "$WORK/slice-false"
	local code
	code=0; ( cd "$REPO" && make slice SLICE="$WORK/slice-true" ) >/dev/null 2>&1 || code=$?
	assert_eq 0 "$code" "make slice with a stub that exits 0"
	code=0; ( cd "$REPO" && make slice SLICE="$WORK/slice-false" ) >/dev/null 2>&1 || code=$?
	assert_eq 2 "$code" "make slice with a stub that exits 3 (make reports its own 2)"
	grep -q 'scripts/slice-arc.sh' "$REPO/Makefile" || fail "the Makefile no longer names scripts/slice-arc.sh"
	[ -x "$REPO/scripts/slice-arc.sh" ] || fail "scripts/slice-arc.sh is not executable"
	ok "make slice is wired to this script and fails when it fails"
}

########################################################################
# PHASE: gate
#
# VS1 proved the gofmt step with ONE mutation — a parseable but misformatted
# file — and recorded the class as closed. The class it did not cover, a file
# gofmt cannot PARSE, walked through `make check` with exit 0 at ee543b9,
# because gofmt exits 2 to STDERR and the recipe tested stdout only. This is
# the standing leg for it, and it is here rather than in the Makefile because a
# target invoking the gate from inside the gate is circular.
#
# The mutations are applied to a COPY of the repository, never in the tree, for
# the reason VS2's harness incident recorded: a mutation left behind poisons
# every later result, and `git checkout` does not restore an untracked file.
########################################################################

phase_gate() {
	phase "gate — make check against a file gofmt cannot parse"

	local copy="$WORK/gate-copy"
	rsync -a --exclude '.git' --exclude '/api' --exclude 'bin' "$REPO/" "$copy/"

	local code
	step "G1: control — the clean copy passes"
	code=0; ( cd "$copy" && make check ) >"$WORK/gate-clean.log" 2>&1 || code=$?
	[ "$code" -eq 0 ] || { tail -20 "$WORK/gate-clean.log" >&2; fail "the clean copy did not pass (exit $code)"; }
	ok "make check on a clean copy = 0"

	# .tools/ is hidden, so `./...` does not match it and neither go build, go
	# vet nor go test can see the file. internal/config's AST sweep skips it too
	# (sweep_test.go). That isolates the gofmt step as the only thing that could
	# catch it — which is exactly the condition under which it did not.
	step "G2: an UNPARSEABLE file in a hidden directory"
	mkdir -p "$copy/.tools"
	printf 'package tools\n\nfunc broken( {\n' >"$copy/.tools/broken.go"
	code=0; ( cd "$copy" && make check ) >"$WORK/gate-unparseable.log" 2>&1 || code=$?
	assert_eq 2 "$code" "make check with an unparseable .go file"
	grep -q 'cannot PARSE' "$WORK/gate-unparseable.log" ||
		fail "the gate failed, but not for the parse reason: $(tail -3 "$WORK/gate-unparseable.log")"
	ok "and it said so: $(grep -m1 'expected' "$WORK/gate-unparseable.log")"
	rm -rf "$copy/.tools"

	step "G3: control — a PARSEABLE but misformatted file (VS1's own mutation)"
	printf '\nfunc  unformatted( ) {\n_ = 1\n}\n' >>"$copy/cmd/api/main.go"
	code=0; ( cd "$copy" && make check ) >"$WORK/gate-misformatted.log" 2>&1 || code=$?
	assert_eq 2 "$code" "make check with a misformatted .go file"
	grep -q 'gofmt -l reported unformatted files' "$WORK/gate-misformatted.log" ||
		fail "the gate failed, but not for the formatting reason"
	ok "and it took the OTHER branch, which is what makes G2 a second leg"
}

########################################################################
# PHASE: arc
########################################################################

# THE BUILD HUNG FOR TEN MINUTES AND HUB WAS NOT THE REASON — VS6's diagnosis is
# corrected here rather than inherited. VS6 recorded `docker compose build api`
# stalling at "resolve image config for docker-image://docker.io/docker/dockerfile:1"
# and concluded egress to registry-1.docker.io did not answer. Measured at VS8 on
# the same machine: registry-1.docker.io answers 401 in 0.17s, which is correct
# for an unauthenticated request, and what hangs is `docker-credential-desktop
# get` — indefinitely, with no output. Killed at 15s, exit 143. With `credsStore`
# removed from a COPY of ~/.docker, the same build finishes in 8.6s.
#
# A HELPER THAT EXITS NON-ZERO HAS ANSWERED. "credentials not found" is exit 1
# and is a perfectly good reply; only the deadline is a failure, so the exit code
# is deliberately not looked at.
cred_helper_answers() {
	local store="$1" pid waited=0
	printf 'https://index.docker.io/v1/\n' | "docker-credential-$store" get >/dev/null 2>&1 &
	pid=$!
	while kill -0 "$pid" 2>/dev/null; do
		if [ "$waited" -ge 10 ]; then
			kill -TERM "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
			return 1
		fi
		sleep 1
		waited=$((waited + 1))
	done
	wait "$pid" 2>/dev/null || true
	return 0
}

# docker_preflight changes NOTHING on a machine whose credential helper answers.
# On one whose helper hangs it says so, in full, and moves this run onto a copy
# of the docker config with `credsStore` deleted — every image this project pulls
# is public, so nothing here needs a credential at all.
docker_preflight() {
	local config_dir="${DOCKER_CONFIG:-$HOME/.docker}" store
	store="$(jq -r '.credsStore // empty' "$config_dir/config.json" 2>/dev/null || true)"
	[ -n "$store" ] || return 0
	command -v "docker-credential-$store" >/dev/null 2>&1 || return 0

	if cred_helper_answers "$store"; then
		ok "docker-credential-$store answers"
		return 0
	fi

	printf '\033[33m     docker-credential-%s did not answer in 10s.\033[0m\n' "$store" >&2
	printf '     Hub itself: registry-1.docker.io/v2/ -> %s in %ss (401 is correct unauthenticated).\n' \
		"$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 https://registry-1.docker.io/v2/ || echo unreachable)" \
		"$(curl -sS -o /dev/null -w '%{time_total}' --max-time 10 https://registry-1.docker.io/v2/ || echo '?')" >&2
	printf '     So the stall is the CREDENTIAL HELPER and not egress, which is what\n' >&2
	printf '     CLAUDE.md recorded at VS6. This run continues against a copy of\n' >&2
	printf '     %s with credsStore removed; restart Docker Desktop to fix it properly.\n' "$config_dir" >&2

	cp -R "$config_dir" "$WORK/docker" 2>/dev/null || true
	mkdir -p "$WORK/docker"
	jq 'del(.credsStore)' "$config_dir/config.json" >"$WORK/docker/config.json"
	export DOCKER_CONFIG="$WORK/docker"
}

api_base() {
	local published
	published="$("${COMPOSE[@]}" port api 8080 2>/dev/null)" || true
	[ -n "$published" ] || fail "the api container did not answer for its own published port"
	printf 'http://%s' "$published"
}

# psql runs INSIDE the postgres container: the arc asserts on stored rows, and
# the host may have no client. `make test-db`'s leg is the one that proves the
# published port, and it is a separate phase.
in_psql() {
	"${COMPOSE[@]}" exec -T postgres psql -U travellog -d travellog -tAc "$1"
}

# THE REQUEST BODIES ARE BUILT BY jq AND NEVER WRITTEN INLINE, AND THAT IS A
# MEASUREMENT RATHER THAN A STYLE. The first draft wrote
# `assert_eq 201 "$(req … -d "{\"email\":\"$ARC_EMAIL\",\"passphrase\":…}")" "…"`.
# Inside a command substitution that is itself inside a quoted ARGUMENT, bash
# does not keep the inner double quotes: the braces reached the shell unquoted
# and BRACE-EXPANDED, so `req` ran TWICE — once with `{"email":…}` and once with
# `{"passphrase":…}` — the server answered 400 invalid_body to both, and the leg
# reported one failure with the wrong label. The identical line as the right-hand
# side of an ASSIGNMENT parses correctly, which is what made it look like a
# server defect. Every status is assigned to a variable first for the same
# reason.
body_json() { jq -cn "$@"; }

phase_arc() {
	phase "arc — register, sign in, write, read, revalidate, restart, read again"
	need curl
	need jq

	local code base token auth_header shouty traveller_id

	step "A0: docker compose down -v — the volume goes, so what follows is real"
	"${COMPOSE[@]}" down -v
	ok "cold: no pgdata"

	step "A1: docker compose build api"
	# `# syntax=docker/dockerfile:1` makes BuildKit resolve that frontend from
	# Hub on every build, which is where both recorded stalls happened. The
	# preflight is what turns a ten-minute silence into a sentence.
	docker_preflight
	if ! "${COMPOSE[@]}" build api; then
		fail "docker compose build api failed — the output above is the reason."
	fi
	ok "the image is built from this tree"

	step "A2: make up — both services healthy"
	( cd "$REPO" && make up )
	assert_eq healthy "$(docker inspect --format '{{.State.Health.Status}}' "$("${COMPOSE[@]}" ps -q postgres)")" "postgres health"
	assert_eq healthy "$(docker inspect --format '{{.State.Health.Status}}' "$("${COMPOSE[@]}" ps -q api)")" "api health"

	base="$(api_base)"
	ok "api published at $base"

	step "A3: GET /healthz"
	code="$(req "$base/healthz")"
	assert_eq 200 "$code" "GET /healthz"
	assert_eq ok "$(jqbody .status)" "healthz status"

	# THE FOUR ROUTES ARE IN THE RUNNING CONTAINER. VS6 and VS7 both recorded
	# that they were green in `go test` and 404 in the container, because the
	# image was never rebuilt. A1 is what closes that, and this is what proves
	# A1 closed it — a 404 here means an image from before VS6.
	step "A4: POST /v1/auth/register"
	code="$(req -X POST "$base/v1/auth/register" -H "$JSON_CT" \
		-d "$(body_json --arg e "$ARC_EMAIL" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 201 "$code" "POST /v1/auth/register"
	assert_eq "$ARC_EMAIL" "$(jqbody .email)" "the registered email"
	assert_eq null "$(jqbody .name)" "name — null, not absent (the client casts it)"
	traveller_id="$(jqbody .id)"
	[ -n "$traveller_id" ] && [ "$traveller_id" != null ] || fail "register returned no id"
	ok "traveller id $traveller_id"

	# DEC-65: the unique index is on lower(email) and every lookup says
	# `lower(email) = lower($1)`. THESE TWO STEPS ARE THE ONLY PLACE IN THE ARC
	# THAT PROVES IT. Register the same address in a different case and the
	# INDEX — not any Go code — is what refuses it; sign in in a different case
	# and the functional LOOKUP is what finds it. Lowercase either request and
	# both steps pass against a plain b-tree on `email`, so the case is the
	# assertion and not decoration.
	shouty="$(printf '%s' "$ARC_EMAIL" | tr 'a-z' 'A-Z')"
	step "A5: POST /v1/auth/register, SAME address UPPERCASED — the index refuses it"
	code="$(req -X POST "$base/v1/auth/register" -H "$JSON_CT" \
		-d "$(body_json --arg e "$shouty" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 409 "$code" "register $shouty"
	assert_eq conflict "$(jqbody .code)" "the code"

	step "A6: POST /v1/auth/session, address UPPERCASED — the functional lookup finds it"
	code="$(req -X POST "$base/v1/auth/session" -H "$JSON_CT" \
		-d "$(body_json --arg e "$shouty" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 201 "$code" "POST /v1/auth/session"
	token="$(jqbody .token)"
	[ -n "$token" ] && [ "$token" != null ] || fail "sign-in returned no token"
	ok "token issued, ${#token} characters"
	auth_header="Authorization: Bearer $token"

	step "A7: GET /v1/logbook before any write — 200 and NO ETag"
	code="$(req -H "$auth_header" "$base/v1/logbook")"
	assert_eq 200 "$code" "GET /v1/logbook at version 0"
	assert_eq "" "$(header ETag)" "ETag at logbook_version 0 (W/\"1-0\" is the tag DEC-49 exists to prevent)"
	assert_eq 0 "$(jqbody '.logbook.trips | length')" "trips"
	assert_eq "[]" "$(jqbody -c '.logbook.cities')" "cities — [] and not null"

	# The body carries shareCoordinates:true, which TripWrite has no slot for
	# (SF6). DEC-13 keeps unknown fields tolerated, so it is not refused — it is
	# simply not heard, and A10 reads the stored flags back to prove it.
	step "A8: PUT /v1/trips/$ARC_TRIP"
	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP" -H "$auth_header" -H "$JSON_CT" -d "$TRIP_BODY")"
	assert_eq 200 "$code" "PUT /v1/trips/$ARC_TRIP"
	assert_eq 'W/"1-1"' "$(header ETag)" "the write's ETag"
	assert_eq "$ARC_TRIP" "$(jqbody .id)" "the written id"
	# THE DEFECT VS7 FOUND BY RUNNING THE BINARY, AND THE REASON THIS LINE IS
	# NOT `jq '.cityIds | length'`. A nil Go slice marshals to `null`, and
	# trip.g.dart reads `(json['cityIds'] as List<dynamic>)` with no null
	# branch, so the client threw on the answer to its own write. `length` is 0
	# for both null and [], so only the raw value separates them.
	assert_eq "[]" "$(jqbody -c .cityIds)" "cityIds on the WRITE's answer"
	assert_eq "2027-05-12T00:00:00.000Z" "$(jqbody .start)" "start — a date column, rendered with milliseconds"

	step "A9: GET /v1/logbook — the trip is there"
	code="$(req -H "$auth_header" "$base/v1/logbook")"
	assert_eq 200 "$code" "GET /v1/logbook"
	assert_eq 'W/"1-1"' "$(header ETag)" "the read's ETag"
	assert_eq "Kyoto in May" "$(jqbody '.logbook.trips[0].name')" "the trip's name"
	assert_eq "[]" "$(jqbody -c '.logbook.trips[0].cityIds')" "cityIds on the READ"
	assert_eq 2 "$(jqbody .version)" "the document's format version"

	# `false|false|false` and not `f|f|f`: VS7's record quotes psql's COLUMN
	# display of a bare boolean, and `boolean || text` casts through
	# `boolean::text`, which is the whole word.
	step "A10: the three sharing flags stayed at their defaults (SF6)"
	assert_eq "false|false|false" \
		"$(in_psql "select share_photos||'|'||share_notes||'|'||share_coordinates from trips where id='$ARC_TRIP'" | tr -d '[:space:]')" \
		"share_photos|share_notes|share_coordinates after a body that asked for true"

	step "A11: GET /v1/logbook with If-None-Match — 304 and a ZERO-BYTE body"
	code="$(req -H "$auth_header" -H 'If-None-Match: W/"1-1"' "$base/v1/logbook")"
	assert_eq 304 "$code" "conditional GET"
	assert_eq 'W/"1-1"' "$(header ETag)" "the 304's ETag"
	# VS7 measured that this half is net/http's guarantee (bodyAllowedForStatus)
	# rather than this handler's, so it is recorded as the arc confirming the
	# stack end to end and NOT as a guard on internal/httpapi.
	assert_eq 0 "$(body_bytes)" "bytes in the 304's body"
	[ -e "$WORK/body" ] || ok "curl created no output file at all"

	step "A12: a tag from another EMITTER does not revalidate"
	code="$(req -H "$auth_header" -H 'If-None-Match: W/"99-1"' "$base/v1/logbook")"
	assert_eq 200 "$code" "If-None-Match: W/\"99-1\""

	step "A13: the four answers only the running container can give"
	code="$(req -H "$auth_header" "$base/v1/nope")"
	assert_eq 404 "$code" "GET /v1/nope"
	assert_eq 'application/json' "$(header Content-Type)" "  its Content-Type — the mux's own 404, brought inside the envelope"
	assert_eq not_found "$(jqbody .code)" "  its code"
	code="$(req -X POST -H "$auth_header" "$base/v1/logbook")"
	assert_eq 405 "$code" "POST /v1/logbook"
	assert_contains "$(header Allow)" GET "  its Allow header"
	code="$(req "$base/v1/logbook")"
	assert_eq 401 "$code" "GET /v1/logbook with no credential"
	code="$(req -H "$auth_header" -H 'X-Logbook-Format: 3' "$base/v1/logbook")"
	assert_eq 406 "$code" "GET with an unwritable format"
	assert_eq 2 "$(header X-Logbook-Format)" "  the formats this build can write"

	########################################################################
	# THE RESTART LEG. `down` and NOT `down -v` — see the header. Everything
	# above proves the API; only this proves pgdata, and its failure mode is
	# invisible until a redeploy destroys somebody's log.
	########################################################################
	step "A14: make down && make up — the stack is torn down and brought back"
	( cd "$REPO" && make down )
	[ -z "$("${COMPOSE[@]}" ps -q 2>/dev/null)" ] || fail "containers survived make down"
	ok "no containers"
	docker volume inspect travellog_pgdata >/dev/null 2>&1 || fail "the named volume did not survive make down"
	ok "travellog_pgdata is still there"
	( cd "$REPO" && make up )
	base="$(api_base)"

	step "A15: GET /v1/logbook after the restart — the SAME token, the SAME trip"
	# The token is not re-issued: sessions live in Postgres too, so a 401 here
	# would mean the sessions table did not survive either.
	code="$(req -H "$auth_header" "$base/v1/logbook")"
	assert_eq 200 "$code" "GET /v1/logbook after restart"
	assert_eq "Kyoto in May" "$(jqbody '.logbook.trips[0].name')" "the trip's name, after a full teardown"
	assert_eq 'W/"1-1"' "$(header ETag)" "the ETag — the version counter survived too"
	assert_eq "[]" "$(jqbody -c '.logbook.trips[0].cityIds')" "cityIds"
	ok "the log outlived the stack"
}

########################################################################
# PHASE: testdb
#
# VS1-FIXES finding 4: `make test-db` printed a hardcoded URL — port 5434,
# user/password/database all literal `travellog` — while compose resolves all
# four from the environment. On this machine 5432 is a developer's own Postgres
# and 5433 an unrelated container, so a stale printed URL can point host-run
# tests that CREATE AND DROP TABLES at a database that is not the stack's.
#
# It runs under its own project name and its own port so it can never disturb
# `make up`, and every value is overridden so a recipe that restated any of
# them would print the wrong one.
########################################################################

phase_testdb() {
	phase "testdb — make test-db derives its URL from the container that is running"

	local project=travellog-slice-testdb
	CLEANUP+=("COMPOSE_PROJECT_NAME=$project POSTGRES_PORT=5999 ${COMPOSE[*]} down -v")

	step "T1: bring postgres up under an override, and read what the target prints"
	local url
	url="$(
		cd "$REPO" &&
		COMPOSE_PROJECT_NAME="$project" POSTGRES_PORT=5999 POSTGRES_USER=alice \
			POSTGRES_PASSWORD=s3cret POSTGRES_DB=otherdb \
			make test-db 2>/dev/null | grep '^export TEST_DATABASE_URL='
	)"
	[ -n "$url" ] || fail "make test-db printed no URL"
	printf '     %s\n' "$url"

	assert_contains "$url" ":5999/" "the printed URL's port"
	assert_contains "$url" "//alice:" "the printed URL's user"
	assert_contains "$url" "s3cret@" "the printed URL's password"
	assert_contains "$url" "/otherdb?" "the printed URL's database"

	step "T2: it agrees with compose's own answer for the running container"
	local resolved
	resolved="$(COMPOSE_PROJECT_NAME="$project" POSTGRES_PORT=5999 "${COMPOSE[@]}" port postgres 5432)"
	assert_contains "$url" ":${resolved##*:}/" "the printed port against \`compose port postgres 5432\` ($resolved)"

	# T2 is string comparison. T3 is what makes it evidence: the URL opens a
	# session as the user and database the stack is actually running.
	step "T3: the URL connects, as the user and database it names"
	if command -v psql >/dev/null 2>&1; then
		assert_eq "alice@otherdb" \
			"$(psql "${url#export TEST_DATABASE_URL=}" -tAc "select current_user||'@'||current_database()" | tr -d '[:space:]')" \
			"the session the printed URL opens"
	else
		printf '     SKIP no psql on PATH — T1 and T2 stand, T3 is the one that needs a client\n'
	fi
}

########################################################################
# PHASE: healthcheck
#
# VS1-FIXES finding 2: `pg_isready` with no `-h` probes the UNIX SOCKET, and
# the official entrypoint runs a bootstrap server on the socket only —
# listen_addresses='' — while it finishes initdb and runs the init scripts.
# Through that phase the check passes and TCP refuses, so
# `depends_on: service_healthy` released the api against a port nothing was
# listening on. Measured then: 12 samples of docker=healthy while TCP=REFUSED.
#
# THE LEG IS DIFFERENTIAL BECAUSE A ONE-SIDED VERSION CANNOT BE TRUSTED. The
# window on a normal cold start was measured at 0.33s against a 3s interval, so
# a poll that simply never disagrees proves the poll is too slow just as well as
# it proves the fix. Running the DEFECT first, and requiring it to disagree, is
# what says the instrument can see the thing.
########################################################################

hc_run() {
	local project="$1" test_cmd="$2" label="$3" want="$4"
	local override="$WORK/hc-$project.yml"
	local initdir="$WORK/hc-init"

	mkdir -p "$initdir"
	# VS4's migrations are what widen the window in production. Here one sleep
	# stands in for them, mounted where the entrypoint runs it — which only
	# happens on a COLD volume, hence the `down -v` below.
	printf '#!/bin/sh\nsleep 15\n' >"$initdir/00-slow.sh"
	chmod +x "$initdir/00-slow.sh"

	cat >"$override" <<-YML
		services:
		  postgres:
		    volumes:
		      - $initdir/00-slow.sh:/docker-entrypoint-initdb.d/00-slow.sh:ro
		    healthcheck:
		      test: ["CMD-SHELL", "$test_cmd"]
	YML

	local compose=(docker compose -f "$REPO/deploy/docker-compose.yml" -f "$override" -p "$project")
	CLEANUP+=("${compose[*]} down -v")
	POSTGRES_PORT=15434 "${compose[@]}" down -v >/dev/null 2>&1 || true
	POSTGRES_PORT=15434 "${compose[@]}" up -d --no-deps postgres >/dev/null

	local cid; cid="$(POSTGRES_PORT=15434 "${compose[@]}" ps -q postgres)"
	local disagreements=0 samples=0 verdict tcp i
	printf '     %-6s %-10s %s\n' "t(s)" "docker" "TCP(-h 127.0.0.1)"
	for i in $(seq 0 40); do
		verdict="$(docker inspect --format '{{.State.Health.Status}}' "$cid" 2>/dev/null || echo gone)"
		if docker exec "$cid" pg_isready -h 127.0.0.1 -U travellog -d travellog >/dev/null 2>&1; then
			tcp=ACCEPT
		else
			tcp=REFUSED
		fi
		samples=$((samples + 1))
		if [ "$verdict" = healthy ] && [ "$tcp" = REFUSED ]; then
			disagreements=$((disagreements + 1))
			printf '     %-6s %-10s %s  <-- DISAGREE\n' "$i" "$verdict" "$tcp"
		elif [ $((i % 5)) -eq 0 ] || [ "$verdict" = healthy ]; then
			printf '     %-6s %-10s %s\n' "$i" "$verdict" "$tcp"
		fi
		[ "$verdict" = healthy ] && [ "$tcp" = ACCEPT ] && break
		sleep 1
	done
	printf '     %s: %s samples, %s with docker=healthy while TCP=REFUSED\n' "$label" "$samples" "$disagreements"

	POSTGRES_PORT=15434 "${compose[@]}" down -v >/dev/null 2>&1 || true

	case "$want" in
	disagrees)
		[ "$disagreements" -gt 0 ] || fail "the socket-only healthcheck did NOT disagree — the poll cannot see the defect it is aimed at, so H2 would prove nothing"
		ok "the defect is visible to this instrument ($disagreements disagreements)" ;;
	agrees)
		[ "$disagreements" -eq 0 ] || fail "$disagreements sample(s) reported healthy while TCP refused"
		ok "docker's verdict and a real TCP probe never disagreed" ;;
	esac
}

# to_ms turns compose's own duration spelling into milliseconds. `docker compose
# config` echoes the file's text ("2s", "1m30s") rather than normalising it, so
# comparing the strings would put 2s below 10s and above 3s at the same time.
to_ms() {
	printf '%s' "$1" | awk '
		{
			total = 0
			while (match($0, /[0-9]+(\.[0-9]+)?(ms|us|ns|s|m|h)/)) {
				chunk = substr($0, RSTART, RLENGTH)
				$0 = substr($0, RSTART + RLENGTH)
				unit = chunk; sub(/^[0-9.]+/, "", unit)
				value = chunk + 0
				if (unit == "h") total += value * 3600000
				else if (unit == "m") total += value * 60000
				else if (unit == "s") total += value * 1000
				else if (unit == "ms") total += value
				else if (unit == "us") total += value / 1000
				else if (unit == "ns") total += value / 1000000
			}
			printf "%d", total
		}'
}

phase_healthcheck() {
	phase "healthcheck — docker's verdict against a real TCP connect, and the budgets"

	step "H1: the DEFECT — pg_isready with no -h, which probes the socket"
	hc_run travellog-slice-hc-defect \
		'pg_isready -U travellog -d travellog' \
		'socket-only (the pre-fix recipe)' disagrees

	step "H2: the SHIPPED healthcheck, same slow init"
	hc_run travellog-slice-hc-fixed \
		'pg_isready -h 127.0.0.1 -U travellog -d travellog' \
		'the shipped recipe' agrees

	# VS4 found postgres shipping `timeout: 3s` against `interval: 2s` — a probe
	# budget longer than the gap between probes — by READING, not by any test.
	# The api image's budgets are guarded by test/image (hc.Timeout >=
	# hc.Interval fails it); the compose ones were guarded by nothing.
	step "H3: every compose healthcheck's timeout is below its interval"
	local budgets svc t i
	budgets="$("${COMPOSE[@]}" config --format json |
		jq -r '.services | to_entries[] | select(.value.healthcheck) |
			"\(.key) \(.value.healthcheck.timeout) \(.value.healthcheck.interval)"')"
	[ -n "$budgets" ] || fail "no service in the compose file declares a healthcheck — this leg would pass vacuously"
	while read -r svc t i; do
		t="$(to_ms "$t")"
		i="$(to_ms "$i")"
		[ "$t" -lt "$i" ] || fail "$svc: healthcheck timeout ${t}ms is not below interval ${i}ms"
		ok "$svc: timeout ${t}ms < interval ${i}ms"
	done <<<"$budgets"
}

########################################################################

main() {
	local phases=("$@")
	[ "${#phases[@]}" -gt 0 ] || phases=(record gate arc testdb healthcheck)

	local p
	for p in "${phases[@]}"; do
		case "$p" in
		record) phase_record ;;
		gate) phase_gate ;;
		arc) phase_arc ;;
		testdb) phase_testdb ;;
		healthcheck) phase_healthcheck ;;
		*) fail "unknown phase $p (record gate arc testdb healthcheck)" ;;
		esac
	done

	printf '\n\033[32mslice-arc: every phase green — %s\033[0m\n' "${phases[*]}"
}

main "$@"
