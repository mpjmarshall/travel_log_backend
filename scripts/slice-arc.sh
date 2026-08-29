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
# AND FROM R4 THE ARC REFUSES THE DEFAULT PROJECT (DEC-92). A0 destroys the
# named volume of whatever project this runs under, and until R4 that volume
# held nothing anybody would miss. R4 is the step that puts a record in it —
# the plan's own premise is "PostgreSQL is the record and the phone is a
# cache" — and five of the eight steps' acceptance checks read
# `make seed && make check && make slice`, which teaches a developer that
# seeding and then wiping is normal. The phases `testdb` and `healthcheck`
# have run under their own COMPOSE_PROJECT_NAME since VS8, so the MAIN phase
# using the live project was the inconsistency and not the pattern.
#
# Run it somewhere else, which is one variable — see the usage line below —
# or say SLICE_DESTROY_VOLUME=1 to mean it. A variable rather than a prompt,
# because the arc has to run unattended; required rather than defaulted,
# because what it prevents is somebody else's photographs.
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
#
#   COMPOSE_PROJECT_NAME=travellog-slice API_PORT=8085 POSTGRES_PORT=5464 \
#     MINIO_PORT=9005 S3_PUBLIC_BASE_URL=http://127.0.0.1:9005 make slice

set -Eeuo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose -f "$REPO/deploy/docker-compose.yml")

# The email is fixed rather than generated because the arc owns the volume: it
# destroys it at the top, so a fixed address is created exactly once per run and
# every psql assertion below can name it.
ARC_EMAIL="arc@travellog.test"
ARC_PASS="correct-horse-battery-staple"
ARC_TRIP="kyoto"

# The bucket the api creates at boot, and a canary to put in it. Both are
# literals here for the reason ARC_EMAIL is one: the arc owns the volume it
# destroyed at A0, so a fixed name is written exactly once per run. The bucket
# name must agree with S3_BUCKET's default in deploy/docker-compose.yml.
ARC_BUCKET="travellog-media"
ARC_CANARY="travellog-arc-canary"

# THE PHOTOGRAPH THE MEDIA ARC UPLOADS, and its sha256 written out.
#
# THE DIGEST IS A LITERAL AND THAT IS THE POINT OF IT. Computing it here with
# `shasum` would make the arc agree with itself: the address IS the content, so
# a literal is what says the server and this script hashed the same bytes. If
# ARC_PHOTO changes, A16 fails on the id and the new digest is in the failure
# message.
#
#   printf '%s' 'travellog-arc-photograph' | shasum -a 256
#   -> 1330026df05364d4054b989efb295eb1661a2d9771aaf1b052d60bcd273a442d
ARC_PHOTO="travellog-arc-photograph"
ARC_DIGEST="1330026df05364d4054b989efb295eb1661a2d9771aaf1b052d60bcd273a442d"
ARC_BYTES=24

# A second address, begun and never uploaded, so A22 can prove that a
# REFERENCE waits for the bytes rather than for the row.
ARC_UNCOMMITTED="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
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

	########################################################################
	# THE FOUR VALUES THE CLIENT MIRRORS, PINNED ON THIS SIDE TOO.
	#
	# The client pins all four by literal already. That proves the CLIENT did
	# not drift on its own and says nothing about this side — and pinning is
	# not agreement: a server that lowers one of these makes the client wrong
	# and green. It goes on accepting a 30 MiB photograph, or asking for a
	# hundred ids, and finds out as a 422 in somebody's hand.
	#
	# THE HONEST SEAM IS CROSS-REPO AND NEITHER SIDE CAN REACH IT. This arc
	# cannot read the Flutter tree (a separate repository, and often absent),
	# and `flutter test` opens no socket. So this is deliberately TWO PINS
	# rather than one comparison: each side reddens its own gate when it
	# moves, which does not prove they agree but does make a silent drift
	# impossible. Somebody changing one gets a red and has to go and look.
	#
	# Closing it properly is a feature rather than a test — the server would
	# serve its own limits and the client would assert against them at
	# runtime. Recorded rather than done.
	########################################################################
	step "R0: the four values the client mirrors have not moved on this side"
	# READ FROM THE PACKAGE, NOT FROM ONE NAMED FILE, and asserted to be found
	# EXACTLY ONCE. Pinning `internal/logbook/walk.go` means a constant moved to
	# a new file reports `= <empty>, want 500` — a red about a value that did
	# not change, which sends a reader hunting for a drift that did not happen.
	# And a doc line repeating the literal would make the grep answer twice, so
	# the count is checked before the value.
	mirrored() {
		local name="$1" hits
		hits="$(grep -rhoE "\b${name} +(int +)?= +[0-9]+" internal/ --include='*.go' |
			grep -v '_test.go' | grep -oE '[0-9]+$' | sort -u)"
		if [ "$(printf '%s\n' "$hits" | grep -c .)" -ne 1 ]; then
			fail "${name} is declared $(printf '%s\n' "$hits" | grep -c .) times in internal/ — this check reads one declaration, not several"
		fi
		printf '%s' "$hits"
	}
	assert_eq 100 "$(mirrored MaxMintIDs)" "MaxMintIDs (client: mediaMintBatchLimit)"
	assert_eq 2 "$(mirrored FormatVersion)" "FormatVersion (client: logbookFormatVersion)"
	assert_eq 500 "$(mirrored MaxWalkPoints)" "MaxWalkPoints (client: walkTrackCap)"
	# MEDIA_MAX_BYTES is the odd one: a DEPLOYMENT value rather than a
	# compile-time constant, so what is pinned here is the shipped default in
	# the compose file, which is what the client's 25 MiB was written against.
	assert_eq 26214400 "$(grep -oE 'MEDIA_MAX_BYTES:-[0-9]+' deploy/docker-compose.yml | grep -oE '[0-9]+$')" \
		"MEDIA_MAX_BYTES default (client: mediaMaxBytes)"

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
	# A doc line is `## <name> — …`. The em dash is what separates a heading
	# from the continuation lines under it, several of which also begin with a
	# lowercase word; without it this compared "normally", "without" and
	# "three" against the target list.
	local documented targets
	documented="$(grep -oE '^## [a-z0-9-]+ —' "$REPO/Makefile" | sed 's/^## //; s/ —$//' | sort -u)"
	targets="$(grep -hoE '^\.PHONY:.*' "$REPO/Makefile" | sed 's/^\.PHONY://' | tr ' ' '\n' | grep -v '^$' | sort -u)"
	if [ "$documented" = "$targets" ]; then
		ok "all $(printf '%s\n' "$targets" | wc -l | tr -d ' ') targets are documented, and every ## heading is a target"
	else
		printf '\033[31m    FAIL the ## headings and .PHONY disagree:\033[0m\n' >&2
		diff <(printf '%s\n' "$documented") <(printf '%s\n' "$targets") | sed 's/^/       /' >&2 || true
		fail "the Makefile documents a target it does not have, or has one it does not document"
	fi

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

	########################################################################
	# R4: THE GUARD THAT STOPS THIS SCRIPT DESTROYING THE LIVE VOLUME.
	#
	# IT IS RUN RATHER THAN READ, and that is the whole point: a grep for
	# SLICE_DESTROY_VOLUME passes against a variable nothing consults, which is
	# exactly the class of check this project has had go green against correct
	# and incorrect code alike. This INVOKES the arc phase under the live
	# project name and asserts it exits non-zero — and it is safe to invoke,
	# because the refusal is the statement before `down -v` rather than after
	# it. The volume's continued existence is asserted afterwards, which is the
	# half a grep can never make.
	########################################################################
	step "R4: the arc refuses the live project, and the live volume survives being asked"
	local live_volume before after code
	live_volume="travellog_pgdata"
	before="$(docker volume inspect "$live_volume" >/dev/null 2>&1 && echo present || echo absent)"

	set +e
	COMPOSE_PROJECT_NAME=travellog SLICE_DESTROY_VOLUME= \
		"$REPO/scripts/slice-arc.sh" arc >"$WORK/refusal" 2>&1
	code=$?
	set -e
	# THE EXIT CODE ALONE IS A VACUOUS ASSERTION AND MUST NOT BE THE WHOLE LEG.
	# Measured: with `refuse_the_live_project` mutated to `return 0` and docker
	# stubbed so nothing could be destroyed, THIS LINE STILL PASSED — the arc
	# ran on past the refusal and died at the first health check, which is also
	# exit 1. The two `assert_contains` below are what actually reddened. So the
	# guard is proved by WHAT THE REFUSAL SAYS, not by the fact that something
	# failed, and anybody tempted to simplify this leg down to its exit code
	# would be deleting the whole of its evidence.
	assert_eq 1 "$code" "the arc's exit code under the live project"
	assert_contains "$(cat "$WORK/refusal")" "SLICE_DESTROY_VOLUME=1" "the refusal names the way to mean it"
	assert_contains "$(cat "$WORK/refusal")" "make backup first" "the refusal names the backup"

	after="$(docker volume inspect "$live_volume" >/dev/null 2>&1 && echo present || echo absent)"
	assert_eq "$before" "$after" "$live_volume across the refusal"

	# AND THE OTHER DIRECTION, WHICH IS WHAT STOPS THE GUARD BEING "REFUSE
	# ALWAYS". A guard that refused every project would pass R4 exactly as well
	# as a correct one, and this arc would then be unrunnable anywhere.
	#
	# It starts the REAL phase under a probe project and stops it as soon as the
	# guard has spoken, because what comes next is a two-minute image build and
	# this phase is the cheap one. The probe project has never existed, so its
	# `down -v` destroys nothing.
	step "R5: a project that is not the live one is not refused"
	local probe=travellog-slice-guardprobe waited
	set +e
	COMPOSE_PROJECT_NAME="$probe" POSTGRES_PORT=15999 API_PORT=15998 MINIO_PORT=15997 \
		"$REPO/scripts/slice-arc.sh" arc >"$WORK/permitted" 2>&1 &
	local probe_pid=$!
	set -e
	CLEANUP+=("kill $probe_pid")
	CLEANUP+=("COMPOSE_PROJECT_NAME=$probe POSTGRES_PORT=15999 API_PORT=15998 MINIO_PORT=15997 ${COMPOSE[*]} down -v")
	waited=0
	while [ "$waited" -lt 60 ]; do
		if grep -q "this arc's own volume\|the project is the live one" "$WORK/permitted" 2>/dev/null; then
			break
		fi
		sleep 1
		waited=$((waited + 1))
	done
	kill "$probe_pid" 2>/dev/null || true
	wait "$probe_pid" 2>/dev/null || true

	assert_contains "$(cat "$WORK/permitted")" "${probe}_pgdata is this arc's own volume" \
		"the guard's verdict on a project that is not the live one"
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

	local code base token auth_header shouty traveller_id url same_address_body

	step "A0: docker compose down -v — the volume goes, so what follows is real"
	refuse_the_live_project
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

	step "A2: make up — all THREE services healthy"
	( cd "$REPO" && make up )
	assert_eq healthy "$(docker inspect --format '{{.State.Health.Status}}' "$("${COMPOSE[@]}" ps -q postgres)")" "postgres health"
	assert_eq healthy "$(docker inspect --format '{{.State.Health.Status}}' "$("${COMPOSE[@]}" ps -q minio)")" "minio health"
	assert_eq healthy "$(docker inspect --format '{{.State.Health.Status}}' "$("${COMPOSE[@]}" ps -q api)")" "api health"

	base="$(api_base)"
	ok "api published at $base"

	########################################################################
	# A2b: THE BUCKET, AND THIS IS WHY THE THREE ASSERTIONS ABOVE ARE NOT IT.
	#
	# `up --wait` reporting three healthy services says NOTHING about whether a
	# photograph can be stored (DEC-98). Measured against the official image at
	# a pinned tag with a fresh volume and no init: /minio/health/live 200,
	# /minio/health/ready 200, `ls /data` -> `.minio.sys` only, ZERO buckets.
	# `MINIO_DEFAULT_BUCKETS` is a Bitnami variable and not a MinIO one. And
	# because presigning is offline arithmetic once the region is pinned, a URL
	# minted against a bucket that does not exist is perfectly well-formed —
	# the failure surfaces on the PHONE, as NoSuchBucket, three steps later.
	#
	# So the arc asserts a ROUND TRIP and not a process. The bucket is here
	# because the api's own boot created it: nothing in compose does, and
	# `mc` never runs except in this step. Delete EnsureBucket from
	# cmd/api/main.go and every assertion above stays green while this one
	# reddens, which is the whole point of it.
	########################################################################
	step "A2b: the api's boot created the bucket, and it round-trips"
	assert_eq 1 "$("${COMPOSE[@]}" exec -T minio mc ls local/ 2>/dev/null | grep -c "$ARC_BUCKET")" \
		"buckets named $ARC_BUCKET in \`mc ls local/\`"
	"${COMPOSE[@]}" exec -T minio sh -c \
		"printf '%s' '$ARC_CANARY' | mc pipe local/$ARC_BUCKET/arc/canary.txt" >/dev/null
	assert_eq "$ARC_CANARY" \
		"$("${COMPOSE[@]}" exec -T minio mc cat "local/$ARC_BUCKET/arc/canary.txt" | tr -d '\r\n')" \
		"the bytes that came back out of the bucket"

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

	# DEC-86 CLOSED REGISTRATION AND THAT MOVED WHAT A5 PROVES. It used to
	# read "the INDEX — not any Go code — is what refuses it", and that is no
	# longer true of this request: `Service.Register` asks whether ANY
	# traveller row exists and refuses before the INSERT is ever attempted, so
	# travellers_email_lower_key is not reached through this route at all. It
	# is still reached, and the leg that reaches it is
	# TestASecondRegistrationOfOneAddressInAnotherCasingIsRefused in
	# internal/postgres, which calls the store directly. Said here because a
	# step whose comment claims the wrong mechanism is the staleness R2 found
	# three times in this file.
	#
	# A6 IS NOW THE ONLY DEC-65 PROOF IN THE ARC, and it is the lookup half:
	# sign in with the address in a different case and the functional index is
	# what finds it. Lowercase that request and the step passes against a plain
	# b-tree on `email`, so the case is the assertion and not decoration.
	shouty="$(printf '%s' "$ARC_EMAIL" | tr 'a-z' 'A-Z')"
	step "A5: POST /v1/auth/register, SAME address UPPERCASED — registration is closed"
	code="$(req -X POST "$base/v1/auth/register" -H "$JSON_CT" \
		-d "$(body_json --arg e "$shouty" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 409 "$code" "register $shouty"
	assert_eq conflict "$(jqbody .code)" "the code"
	same_address_body="$(cat "$WORK/body")"

	# DEC-86, AND IT IS THE STEP THE OLD A5 COULD NOT MAKE. Ruling 3 is
	# single-user; before this, a stranger who reached a deployed instance
	# after the owner had registered got an authenticated account carrying a
	# 600/min traveller budget and, from R6, a `?photos=delete`. The BYTE
	# COMPARISON is the half that matters: the security lens flagged
	# 409-on-duplicate as an enumeration surface, and what closes it is the two
	# answers being the same answer, not the status alone.
	step "A5b: POST /v1/auth/register, a DIFFERENT address — closed, and indistinguishable"
	code="$(req -X POST "$base/v1/auth/register" -H "$JSON_CT" \
		-d "$(body_json --arg e "a-total-stranger@example.com" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 409 "$code" "register a stranger"
	assert_eq conflict "$(jqbody .code)" "the code"
	assert_eq "$same_address_body" "$(cat "$WORK/body")" \
		"the stranger's refusal, byte for byte against the same-address refusal"

	# AND A MALFORMED BODY IS STILL A 422 NAMING THE FIELD. Registration being
	# closed must not swallow the answer a client can act on: 409 says stop
	# trying, and 422 says fix the body.
	step "A5c: POST /v1/auth/register with a malformed address — still 422, still names the field"
	code="$(req -X POST "$base/v1/auth/register" -H "$JSON_CT" \
		-d "$(body_json --arg e "not-an-address" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 422 "$code" "register with a malformed address"
	assert_eq email "$(jqbody .field)" "the field the 422 names"

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
	assert_eq "" "$(header ETag)" "ETag at logbook_version 0 (W/\"2-0\" is the tag DEC-49 exists to prevent)"
	assert_eq 0 "$(jqbody '.logbook.trips | length')" "trips"
	assert_eq "[]" "$(jqbody -c '.logbook.cities')" "cities — [] and not null"

	# The body carries shareCoordinates:true, which TripWrite has no slot for
	# (SF6). DEC-13 keeps unknown fields tolerated, so it is not refused — it is
	# simply not heard, and A10 reads the stored flags back to prove it.
	# THE `2` IN EVERY ETag BELOW IS internal/logbook's EmitterVersion, AND IT
	# IS A LITERAL ON PURPOSE. R1 moved it from 1 to 2 (DEC-91: the emitted
	# Trip gained `shared`, so the CODE's shape changed while the WIRE's did
	# not) and left these five assertions reading `W/"1-1"` — so the arc was
	# red from R1 and R2 is what found it. Reading the constant out of the
	# source instead would make these lines unfalsifiable: a client caches this
	# exact string, so an emitter bump SHOULD break the arc. What it must not
	# do is break it silently a step later.
	step "A8: PUT /v1/trips/$ARC_TRIP"
	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP" -H "$auth_header" -H "$JSON_CT" -d "$TRIP_BODY")"
	assert_eq 200 "$code" "PUT /v1/trips/$ARC_TRIP"
	assert_eq 'W/"2-1"' "$(header ETag)" "the write's ETag"
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
	assert_eq 'W/"2-1"' "$(header ETag)" "the read's ETag"
	assert_eq "Kyoto in May" "$(jqbody '.logbook.trips[0].name')" "the trip's name"
	assert_eq "[]" "$(jqbody -c '.logbook.trips[0].cityIds')" "cityIds on the READ"
	assert_eq 2 "$(jqbody .version)" "the document's format version"

	# `true|true|false` and not `t|t|f`: VS7's record quotes psql's COLUMN
	# display of a bare boolean, and `boolean || text` casts through
	# `boolean::text`, which is the whole word.
	#
	# THE FIRST TWO ARE `true` SINCE R1 AND THIS LINE SAID `false` UNTIL R2.
	# Migration 0002 (DEC-82, PD-01) gave share_photos and share_notes the
	# CLIENT's own defaults, and the arc was never re-run against it — the same
	# staleness as the ETag at A8, found the same way. The leg still says what
	# it was written to say, and says it through the THIRD flag: the request
	# body carries `shareCoordinates:true`, TripWrite has no slot for it, and
	# the stored value is still `false`. An unknown field is tolerated (DEC-13)
	# and is simply not heard.
	step "A10: the three sharing flags stayed at their defaults (SF6)"
	assert_eq "true|true|false" \
		"$(in_psql "select share_photos||'|'||share_notes||'|'||share_coordinates from trips where id='$ARC_TRIP'" | tr -d '[:space:]')" \
		"share_photos|share_notes|share_coordinates after a body that asked for true"

	step "A11: GET /v1/logbook with If-None-Match — 304 and a ZERO-BYTE body"
	code="$(req -H "$auth_header" -H 'If-None-Match: W/"2-1"' "$base/v1/logbook")"
	assert_eq 304 "$code" "conditional GET"
	assert_eq 'W/"2-1"' "$(header ETag)" "the 304's ETag"
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
	# `unsupported_route` AND NOT `not_found` SINCE R1 (DEC-103), and this line
	# said the old word until R2 — the third staleness the arc carried out of
	# R1, beside A8's ETag and A10's defaults. The distinction is the whole
	# reason the word was added: `not_found` is "that trip is not in your log"
	# and this is "this build does not have that route", which a client acts on
	# differently — one is a wrong id, the other needs a newer server.
	assert_eq unsupported_route "$(jqbody .code)" "  its code"
	code="$(req -X POST -H "$auth_header" "$base/v1/logbook")"
	assert_eq 405 "$code" "POST /v1/logbook"
	assert_contains "$(header Allow)" GET "  its Allow header"
	code="$(req "$base/v1/logbook")"
	assert_eq 401 "$code" "GET /v1/logbook with no credential"
	code="$(req -H "$auth_header" -H 'X-Logbook-Format: 3' "$base/v1/logbook")"
	assert_eq 406 "$code" "GET with an unwritable format"
	assert_eq 2 "$(header X-Logbook-Format)" "  the formats this build can write"

	########################################################################
	# A16-A21: THE MEDIA ARC (PD-04, R3). begin -> presigned PUT -> commit ->
	# reference -> mint -> fetch, against the running container and the running
	# MinIO.
	#
	# WHY IT IS HERE AND NOT ONLY IN `go test`. VS6 and VS7 both shipped routes
	# green in `go test` and answering 404 in the container. And the handler
	# legs run against media.Memory, whose URLs are `memory.invalid` on purpose
	# — so NOTHING in `go test ./...` has ever put a byte through a real
	# signature from outside the test binary. The two things only this can say
	# are that the routes are mounted and that a URL this server minted is a
	# URL a client can actually use: everything about the signature is decided
	# by the HOST it covers, and DEC-42's two addresses are two variables that
	# can be swapped with every unit test still green.
	########################################################################
	local digest headers put_status begun

	step "A16: POST /v1/media — the three routes are in the running container"
	code="$(req -X POST "$base/v1/media" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg d "$ARC_DIGEST" --argjson n "$ARC_BYTES" \
			'{sha256:$d,byteSize:$n,contentType:"image/png"}')")"
	assert_eq 201 "$code" "POST /v1/media"
	assert_eq "$ARC_DIGEST" "$(jqbody .id)" "the id — the address IS the digest"
	assert_eq false "$(jqbody .alreadyExists)" "alreadyExists on a first begin"
	begun="$(body)"
	url="$(printf '%s' "$begun" | jq -r .uploadUrl)"
	[ -n "$url" ] && [ "$url" != null ] || fail "begin minted no uploadUrl"

	# THE MAP AND THE SIGNATURE AGREE, FROM OUTSIDE THE TEST BINARY (DEC-88).
	# The handler leg asserts this against media.Memory, which does not sign at
	# all; this reads X-Amz-SignedHeaders out of a URL a REAL SigV4 signer
	# produced. Equality in both directions, because a map with an extra header
	# is as broken as one with a missing header.
	step "A17: uploadHeaders is exactly X-Amz-SignedHeaders minus host"
	local signed handed
	signed="$(printf '%s' "$url" | sed -n 's/.*X-Amz-SignedHeaders=\([^&]*\).*/\1/p' |
		tr ';' '\n' | sed 's/%3B/\n/g' | grep -v '^host$' | sort | tr '\n' ';')"
	handed="$(printf '%s' "$begun" | jq -r '.uploadHeaders | keys[]' | sort | tr '\n' ';')"
	[ -n "$signed" ] || fail "the URL signs host and nothing else — that is what a URL from one of the two BANNED presign calls looks like"
	assert_eq "$signed" "$handed" "the header map's key set against the URL's own signed set"

	# THE UPLOAD, THROUGH THE MINTED URL, REPLAYING THE MAP VERBATIM. This is
	# the one assertion in the repository that says a phone could actually use
	# what this server hands out: the signature covers the HOST, so
	# S3_PUBLIC_BASE_URL being wrong is a 403 here and a green suite everywhere
	# else.
	step "A18: PUT the bytes through the presigned URL"
	local -a header_args=()
	while IFS=$'\t' read -r name value; do
		header_args+=(-H "$name: $value")
	done < <(printf '%s' "$begun" | jq -r '.uploadHeaders | to_entries[] | "\(.key)\t\(.value)"')
	printf '%s' "$ARC_PHOTO" >"$WORK/photo"
	put_status="$(curl -sS -o "$WORK/put" -w '%{http_code}' -X PUT --data-binary "@$WORK/photo" \
		"${header_args[@]}" "$url")"
	assert_eq 200 "$put_status" "PUT to the presigned URL"

	# DEC-88's WRITE-ONCE, AT THE BUCKET AND NOT IN A COMMENT. `If-None-Match:
	# *` is signed in, so a second PUT is 412 PreconditionFailed with the
	# original bytes intact — and THE COMMIT PATH READS 412 AS SUCCESS, which
	# is the sentence docs/CLIENT-PREREQUISITES.md carries. Without it a client
	# retrying after an unacknowledged success reports a failure.
	step "A19: a SECOND PUT at the same address is 412, not a silent replacement"
	put_status="$(curl -sS -o "$WORK/put2" -w '%{http_code}' -X PUT --data-binary "@$WORK/photo" \
		"${header_args[@]}" "$url")"
	assert_eq 412 "$put_status" "the second PUT"

	step "A20: POST /v1/media/{id}/commit, then again — the second is 200"
	code="$(req -X POST "$base/v1/media/$ARC_DIGEST/commit" -H "$auth_header")"
	assert_eq 200 "$code" "commit"
	assert_eq true "$(jqbody .alreadyExists)" "alreadyExists after the commit"
	code="$(req -X POST "$base/v1/media/$ARC_DIGEST/commit" -H "$auth_header")"
	assert_eq 200 "$code" "a SECOND commit — the retry contract, not a 409"

	# THE REFERENCE, WHICH IS WHAT THE WHOLE FLOW IS FOR. It goes through
	# `PUT /v1/trips/{id}`, and the cover check is a COMMITTED check rather
	# than an existence check — so this assertion is only reachable because
	# A20 ran.
	step "A21: the committed object can be a trip's cover, and mint answers a URL that fetches it"
	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg c "$ARC_DIGEST" '{name:"Kyoto in May",coverAsset:$c}')")"
	assert_eq 200 "$code" "PUT /v1/trips/$ARC_TRIP with the committed cover"
	assert_eq "$ARC_DIGEST" "$(jqbody .coverAsset)" "the stored cover"

	code="$(req -X POST "$base/v1/media/mint" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg d "$ARC_DIGEST" '{ids:[$d]}')")"
	assert_eq 200 "$code" "POST /v1/media/mint"
	# DEC-51's TWO HEADERS, ON A RESPONSE THAT CARRIES A BEARER CAPABILITY, and
	# asserted on PRESENCE. A presigned URL is unlimited replay and cannot be
	# revoked before it expires: `no-store` keeps it out of an intermediary's
	# cache and `no-referrer` keeps it out of the next site's logs.
	assert_eq 'no-store, private' "$(header Cache-Control)" "  Cache-Control on the mint"
	assert_eq 'no-referrer' "$(header Referrer-Policy)" "  Referrer-Policy on the mint"
	local read_url
	read_url="$(jqbody -r '.urls[0]')"
	assert_contains "$read_url" 'response-content-disposition=attachment' \
		"  the minted URL's disposition (DEC-51: a mislabelled object is downloaded, never rendered)"

	code="$(curl -sS -o "$WORK/fetched" -D "$WORK/head" -w '%{http_code}' "$read_url")"
	assert_eq 200 "$code" "GET the minted URL"
	assert_eq "$ARC_PHOTO" "$(cat "$WORK/fetched")" "the bytes that came back, byte for byte"

	step "A22: a reference to an object that has NOT been committed is refused by name"
	code="$(req -X POST "$base/v1/media" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg d "$ARC_UNCOMMITTED" --argjson n "$ARC_BYTES" \
			'{sha256:$d,byteSize:$n,contentType:"image/png"}')")"
	assert_eq 201 "$code" "begin a second object and never upload it"
	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg c "$ARC_UNCOMMITTED" '{name:"Kyoto in May",coverAsset:$c}')")"
	assert_eq 422 "$code" "PUT /v1/trips with an UNCOMMITTED cover"
	assert_eq invalid_field "$(jqbody .code)" "  its code"
	assert_eq coverAsset "$(jqbody .field)" "  its field"
	# AND THE ALLOWLIST, WHICH THE SCHEMA ENFORCES TOO — the psql half of that
	# claim is the acceptance check's, and this is the Go half in the container.
	code="$(req -X POST "$base/v1/media" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --argjson n "$ARC_BYTES" \
			'{sha256:"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",byteSize:$n,contentType:"text/html"}')")"
	assert_eq 422 "$code" "begin with contentType text/html"
	assert_eq contentType "$(jqbody .field)" "  its field"

	########################################################################
	# THE RESTART LEG. `down` and NOT `down -v` — see the header. Everything
	# above proves the API; only this proves pgdata, and its failure mode is
	# invisible until a redeploy destroys somebody's log.
	########################################################################
	step "A14: make down && make up — the stack is torn down and brought back"
	( cd "$REPO" && make down )
	[ -z "$("${COMPOSE[@]}" ps -q 2>/dev/null)" ] || fail "containers survived make down"
	ok "no containers"
	# THE VOLUME NAME IS DERIVED AND NOT WRITTEN, which is the correction
	# `make test-db` already carries: this line said `travellog_pgdata`, so the
	# whole arc could only ever run under the DEFAULT project — and the one
	# thing you want when a live stack is holding somebody's log is to run it
	# somewhere else. Compose answers for the project that is actually
	# configured, so COMPOSE_PROJECT_NAME now moves the arc wholesale.
	local project pgvolume
	project="$("${COMPOSE[@]}" config --format json | jq -r '.name')"
	[ -n "$project" ] && [ "$project" != null ] || fail "compose did not answer for its own project name"
	pgvolume="${project}_pgdata"
	docker volume inspect "$pgvolume" >/dev/null 2>&1 || fail "the named volume $pgvolume did not survive make down"
	ok "$pgvolume is still there"
	( cd "$REPO" && make up )
	base="$(api_base)"

	step "A15: GET /v1/logbook after the restart — the SAME token, the SAME trip"
	# The token is not re-issued: sessions live in Postgres too, so a 401 here
	# would mean the sessions table did not survive either.
	code="$(req -H "$auth_header" "$base/v1/logbook")"
	assert_eq 200 "$code" "GET /v1/logbook after restart"
	assert_eq "Kyoto in May" "$(jqbody '.logbook.trips[0].name')" "the trip's name, after a full teardown"
	# `2-2` AND NOT `2-1` SINCE R3, AND THE SECOND HALF IS THE ASSERTION. A21
	# writes the trip a SECOND time to give it its cover, so logbook_version is
	# 2 by the time the stack is torn down — and this line is what says the
	# COUNTER survived rather than merely the row. The literal moved with the
	# arc that moved it, in the same commit, which is the thing R2 found three
	# times that R1 had not done.
	assert_eq 'W/"2-2"' "$(header ETag)" "the ETag — the version counter survived too"
	assert_eq "[]" "$(jqbody -c '.logbook.trips[0].cityIds')" "cityIds"
	# AND THE COVER, which is the only thing in this arc that outlives the
	# stack on BOTH sides of the seam: the media_objects row is in pgdata and
	# the bytes are in miniodata, and a `down` that kept one and lost the other
	# is a reference that resolves and points at nothing.
	assert_eq "$ARC_DIGEST" "$(jqbody -r '.logbook.trips[0].coverAsset')" \
		"the trip's cover, after a full teardown"
	code="$(req -X POST "$base/v1/media/mint" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg d "$ARC_DIGEST" '{ids:[$d]}')")"
	assert_eq 200 "$code" "POST /v1/media/mint after the restart"
	code="$(curl -sS -o "$WORK/fetched" -w '%{http_code}' "$(jqbody -r '.urls[0]')")"
	assert_eq 200 "$code" "GET the re-minted URL — miniodata survived too"
	assert_eq "$ARC_PHOTO" "$(cat "$WORK/fetched")" "the bytes, after a full teardown"
	ok "the log outlived the stack"

	########################################################################
	# A24-A29 AND A31-A33: R5's SIX ROUTES AND R6's THREE, IN THE RUNNING
	# CONTAINER.
	#
	# WHY THEY ARE HERE AND NOT ONLY IN `go test`. VS6 and VS7 both shipped
	# routes that were green in `go test` and answered 404 in the container,
	# because the image was never rebuilt — and R5 ships the first DESTRUCTIVE
	# route in the plan. A cascade that is not mounted is a delete that reports
	# success and removes nothing, which is exactly the branch DEC-103 exists
	# to stop the client taking.
	#
	# WHAT THE ARC COULD NOT SAY AT R5, AND NOW CAN. D3's own promise — "N pins
	# in … KEPT", including the pin whose only visits were on the deleted trip
	# — needs a PLACE, and nothing in this build could create one until R6's
	# `PUT /v1/places/{id}`. R5 recorded that in terms: "R6 is where this step
	# grows its pin." A31-A33 below create the city, the pin and its two
	# occasions, and A29 now asserts the pin survives the cascade with
	# `visits: []`.
	#
	# THE ROW COUNTS ARE STILL `go test ./internal/seed/`'s, at fixture scale
	# against the client's own log — 96 photographs, 5 pins, 18 itinerary rows.
	# What the arc adds is the half `go test` cannot: that the DEPLOYED IMAGE
	# does it.
	########################################################################
	local second_trip="arc-share" share_token="arcsharetoken"

	step "A24: PUT /v1/trips/$second_trip — a second trip, so A15's is left alone"
	code="$(req -X PUT "$base/v1/trips/$second_trip" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{name:"Arc share trip"}')")"
	assert_eq 200 "$code" "PUT /v1/trips/$second_trip"
	assert_eq false "$(jqbody .shared)" "shared — nothing has minted a link yet (DEC-91)"

	# THE PRIVACY SEQUENCE, END TO END, IN THE CONTAINER: coordinates ON, stop,
	# new link — and the new link must not carry coordinates. Removing the
	# reset is a privacy leak rather than a tidiness issue: the next link hands
	# out exact pins without anybody having turned that on.
	step "A25: PUT /v1/trips/$second_trip/share — one switch, and only one"
	code="$(req -X PUT "$base/v1/trips/$second_trip/share" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{shareCoordinates:true}')")"
	assert_eq 200 "$code" "PUT share"
	assert_eq true "$(jqbody .shareCoordinates)" "shareCoordinates"
	# DEC-89 IN THE CONTAINER: the body named one flag, and the other two are
	# where migration 0002 left them.
	assert_eq true "$(jqbody .sharePhotos)" "sharePhotos — not named by the body, not touched"
	assert_eq true "$(jqbody .shareNotes)" "shareNotes — not named by the body, not touched"

	step "A26: DELETE /v1/trips/$second_trip/share — the switches go back to the client's defaults"
	code="$(req -X DELETE "$base/v1/trips/$second_trip/share" -H "$auth_header")"
	assert_eq 200 "$code" "DELETE share"
	assert_eq "true|true|false" \
		"$(in_psql "select share_photos||'|'||share_notes||'|'||share_coordinates from trips where id='$second_trip'" | tr -d '[:space:]')" \
		"the three flags, read out of the ROW rather than out of the answer"

	step "A27: POST /v1/trips/$second_trip/share — the new link is disarmed, and the token is a hash on disk"
	code="$(req -X POST "$base/v1/trips/$second_trip/share" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$share_token" '{token:$t}')")"
	assert_eq 201 "$code" "POST share"
	assert_eq false "$(jqbody .shareCoordinates)" \
		"shareCoordinates on a NEW link after stopping — a killed link must not leave it armed"
	assert_eq "$share_token" "$(jqbody -r .shareLinkId)" "shareLinkId — the token the client supplied"
	assert_eq true "$(jqbody .shared)" "shared (DEC-91)"
	# DEC-85, AGAINST THE ROW RATHER THAN AGAINST THE ANSWER. The whole table is
	# rendered as text, so a `token` column somebody adds back beside the hash
	# is caught by the same assertion.
	assert_eq 0 "$(in_psql "select count(*) from share_links where share_links::text like '%$share_token%'" | tr -d '[:space:]')" \
		"rows of share_links holding the plaintext token"
	assert_eq 1 "$(in_psql "select count(*) from information_schema.columns where table_name='share_links' and column_name='token_hash'" | tr -d '[:space:]')" \
		"share_links.token_hash exists"
	assert_eq 0 "$(in_psql "select count(*) from information_schema.columns where table_name='share_links' and column_name='token'" | tr -d '[:space:]')" \
		"share_links.token — GONE, which is the whole of DEC-85's security claim"

	step "A28: PATCH /v1/me — the name lands, and an empty one is refused without clearing it"
	code="$(req -X PATCH "$base/v1/me" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{name:"Matt"}')")"
	assert_eq 200 "$code" "PATCH /v1/me"
	assert_eq Matt "$(jqbody -r .name)" "the name"
	code="$(req -X PATCH "$base/v1/me" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{name:"   "}')")"
	assert_eq 422 "$code" "PATCH /v1/me with an empty name"
	assert_eq name "$(jqbody -r .field)" "  the field it names"
	assert_eq Matt "$(in_psql "select name from travellers" | tr -d '[:space:]')" \
		"the stored name after a refused write — an empty name is not a way to clear it"

	########################################################################
	# A31-A34: R6's THREE ROUTES, AND THE ROW R5 COULD NOT PROVE HERE.
	#
	# THE NUMBERS ARE LABELS AND NOT AN ORDER. A23 has run last since R4 and
	# says so; these run BEFORE A29 because they are what gives A29 something
	# to keep. R5's own record: "D3's 'pins are kept' row, IN THE CONTAINER.
	# The arc cannot create a place until R6 ships PUT /v1/places/{id}, so the
	# cascade's row counts are proved only in `go test ./internal/seed/`. That
	# is a real guard; it is not the running stack. R6 is where the arc grows
	# its pin." This is that pin.
	########################################################################
	local arc_city="arc-kyoto" arc_pin="arc-fushimi" arc_doomed="arc-tofuku"

	step "A31: PUT /v1/cities/$arc_city — with attachTo it answers the WHOLE log"
	code="$(req -X PUT "$base/v1/cities/$arc_city" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$second_trip" \
			'{name:"Kyoto",country:{code:"JP",name:"Japan"},centre:{lat:35.0116,lng:135.7681},attachTo:$t}')")"
	assert_eq 200 "$code" "PUT /v1/cities/$arc_city"
	assert_eq 2 "$(jqbody .version)" "  the format version — this is an ENVELOPE and not a bare city"
	assert_eq "$arc_city" "$(jqbody -r ".logbook.trips[] | select(.id==\"$second_trip\") | .cityIds[-1]")" \
		"  the LAST id in the trip's itinerary — the client appends at the end"

	step "A31b: PUT /v1/cities/$arc_city again, with NO attachTo — a bare city"
	code="$(req -X PUT "$base/v1/cities/$arc_city" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{name:"Kyoto"}')")"
	assert_eq 200 "$code" "PUT /v1/cities/$arc_city with only a name"
	assert_eq null "$(jqbody -c .logbook)" "  .logbook — absent, because nothing but the city moved"
	assert_eq JP "$(jqbody -r .country.code)" "  country — DEC-89: a rename leaves it alone"
	assert_eq 1 "$(in_psql "select count(*) from trip_cities where city_id='$arc_city'" | tr -d '[:space:]')" \
		"  rows in trip_cities — a re-PUT does not attach twice"

	step "A32: PUT /v1/places/$arc_pin — C1's pin, and a bare Place never reaches the wire"
	code="$(req -X PUT "$base/v1/places/$arc_pin" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg c "$arc_city" \
			'{cityId:$c,name:"Fushimi Inari",coordinates:{lat:34.9671,lng:135.7727}}')")"
	assert_eq 200 "$code" "PUT /v1/places/$arc_pin"
	# CF-BLO-3, AGAINST THE WIRE RATHER THAN AGAINST A Go VALUE. `jq -c` renders
	# a JSON null as the four characters `null`, so this tells `[]` from `null`
	# — which is the whole distinction, and the one the client throws on.
	assert_eq "[]" "$(jqbody -c .visits)" "  .visits — [] and NEVER null, on a wishlist pin"

	step "A32b: the whole ordered visits array, and a no-op re-send of it"
	code="$(req -X PUT "$base/v1/places/$arc_pin" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_pin" --arg t "$second_trip" \
			'{visits:[{id:"arc-visit-2",placeId:$p,tripId:$t,at:"2027-10-02T09:00:00.000Z",note:"the torii at dawn"},
			          {id:"arc-visit-1",placeId:$p,tripId:$t,at:"2027-10-01T09:00:00.000Z",note:null}]}')")"
	assert_eq 200 "$code" "PUT $arc_pin with two occasions"
	assert_eq "arc-visit-2" "$(jqbody -r '.visits[0].id')" "  newest first — the client reads visits.first.at as lastVisited"
	assert_eq 2 "$(in_psql "select count(*) from visits where place_id='$arc_pin'" | tr -d '[:space:]')" "  rows in visits"
	# THE RE-SEND, WHICH IS WHAT THE CLIENT DOES WHENEVER A SCREEN RE-SAVES A
	# PLACE IT DID NOT CHANGE. Delete-then-insert of these two IDENTICAL rows
	# leaves both here and nulls photos.visit_id on everything filed to them.
	code="$(req -X PUT "$base/v1/places/$arc_pin" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_pin" --arg t "$second_trip" \
			'{visits:[{id:"arc-visit-2",placeId:$p,tripId:$t,at:"2027-10-02T09:00:00.000Z",note:"the torii at dawn"},
			          {id:"arc-visit-1",placeId:$p,tripId:$t,at:"2027-10-01T09:00:00.000Z",note:null}]}')")"
	assert_eq 200 "$code" "the same two occasions, re-sent unchanged"
	assert_eq 2 "$(in_psql "select count(*) from visits where place_id='$arc_pin'" | tr -d '[:space:]')" \
		"  rows in visits after a no-op re-send"
	assert_eq "the torii at dawn" \
		"$(in_psql "select note from visits where id='arc-visit-2'" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')" \
		"  the note on the re-sent occasion — a note is the traveller's own words"
	# DEC-89's OTHER TWO ANSWERS, from the outside.
	code="$(req -X PUT "$base/v1/places/$arc_pin" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{plan:"go at first light"}')")"
	assert_eq 200 "$code" "PUT $arc_pin with the visits key OMITTED"
	assert_eq 2 "$(jqbody '.visits | length')" "  .visits after a body that never mentioned them"
	code="$(req -X PUT "$base/v1/places/$arc_pin" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{visits:[]}')")"
	assert_eq 422 "$code" "PUT $arc_pin with visits: []"
	assert_eq visits "$(jqbody -r .field)" "  the field it names"
	assert_eq 2 "$(in_psql "select count(*) from visits where place_id='$arc_pin'" | tr -d '[:space:]')" \
		"  rows in visits after the refusal — asserted on the ROW COUNT and not on the status"

	step "A33: DELETE /v1/places/$arc_doomed — the parameter is REQUIRED"
	code="$(req -X PUT "$base/v1/places/$arc_doomed" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg c "$arc_city" \
			'{cityId:$c,name:"Tofuku-ji",coordinates:{lat:34.9761,lng:135.7741}}')")"
	assert_eq 200 "$code" "PUT /v1/places/$arc_doomed"
	code="$(req -X DELETE "$base/v1/places/$arc_doomed" -H "$auth_header")"
	assert_eq 422 "$code" "DELETE /v1/places/$arc_doomed with no ?photos"
	assert_eq photos "$(jqbody -r .field)" "  the field it names"
	code="$(req -X DELETE "$base/v1/places/$arc_doomed?photos=keepp" -H "$auth_header")"
	assert_eq 422 "$code" "DELETE with ?photos=keepp"
	assert_eq 1 "$(in_psql "select count(*) from places where id='$arc_doomed'" | tr -d '[:space:]')" \
		"  the place after two refusals — a request that never said how far to reach must reach nothing"
	code="$(req -X DELETE "$base/v1/places/$arc_doomed?photos=keep" -H "$auth_header")"
	assert_eq 200 "$code" "DELETE with ?photos=keep"
	assert_eq 2 "$(jqbody .version)" "  the format version — D2 answers the WHOLE envelope, not a 204"
	assert_eq 0 "$(in_psql "select count(*) from places where id='$arc_doomed'" | tr -d '[:space:]')" "  the place"
	assert_eq 1 "$(in_psql "select count(*) from places where id='$arc_pin'" | tr -d '[:space:]')" \
		"  the OTHER pin — a removal takes one place and not a city's worth"
	# THE REPEAT IS A SUCCESS, which is what stops a retried delete taking the
	# client's failure branch (DEC-103).
	code="$(req -X DELETE "$base/v1/places/$arc_doomed?photos=keep" -H "$auth_header")"
	assert_eq 200 "$code" "the SECOND DELETE of the same place"

	step "A29: DELETE /v1/trips/$second_trip — the WHOLE logbook comes back"
	code="$(req -X DELETE "$base/v1/trips/$second_trip" -H "$auth_header")"
	assert_eq 200 "$code" "DELETE /v1/trips/$second_trip"
	# THE ENVELOPE AND NOT A BARE TRIP, AND NOT A 204: the cache cannot splice
	# a cascade.
	assert_eq 2 "$(jqbody .version)" "the document's format version — this is an ENVELOPE"
	assert_eq 1 "$(jqbody '.logbook.trips | length')" "trips left"
	assert_eq "$ARC_TRIP" "$(jqbody -r '.logbook.trips[0].id')" "  and it is the other one"
	# "[] AND NOT NULL, ON THE WRITE PATH TOO" MOVED FROM `cities` TO `walks`,
	# AND THE MOVE IS THE HONEST FIX RATHER THAN A DELETION. The claim is that
	# Emit normalises an EMPTY list to `[]` and never to `null` — the shape
	# `logbook.g.dart` throws on — so it needs a list that is actually empty.
	# A31 creates a city, so `cities` stopped being one; `walks` is untouched
	# by anything in this arc UP TO HERE and demonstrates exactly the same rule.
	#
	# "UP TO HERE" IS NEW AT R7 AND IS THE HONEST FORM. R7's A35-A40 create a
	# walk, so this claim is now about a POINT in the arc rather than about the
	# whole of it — which is why that block runs after this step rather than
	# before, and which the next step to write a walk earlier has to read
	# before moving anything.
	assert_eq "[]" "$(jqbody -c '.logbook.walks')" "walks — [] and not null, on the write path too"
	# AND THE CITY SURVIVES THE CASCADE, which is what `cities` is now good for
	# and is a stronger claim than the one it carried. Nothing from `trips`
	# reaches `cities` at all, and trip_cities_city_fk is RESTRICT (DEC-57).
	assert_eq 1 "$(jqbody '[.logbook.cities[] | select(.id=="'"$arc_city"'")] | length')" \
		"  the city, still in the answered log — a deleted trip takes its itinerary and never a city"
	assert_eq 0 "$(in_psql "select count(*) from share_links where trip_id='$second_trip'" | tr -d '[:space:]')" \
		"share_links rows for the deleted trip — the link dies because the link is on the trip"
	# D3's "N pins in … — KEPT" ROW, IN THE RUNNING CONTAINER, AND THIS IS WHAT
	# R5 RECORDED AS GUARDED BY NOTHING HERE. `arc-fushimi` carried BOTH of its
	# occasions on the trip that has just gone, so it is exactly the `gamcheon`
	# shape: a pin whose every visit was on the deleted trip survives with none,
	# which is a wishlist place and is what the sheet promised. The counts are
	# proved at fixture scale in `go test ./internal/seed/`; what is proved HERE
	# is that the deployed image does it.
	assert_eq "$arc_pin" "$(jqbody -r ".logbook.places[] | select(.id==\"$arc_pin\") | .id")" \
		"the pin, in the answered envelope — a trip owns its visits, not its places"
	assert_eq "[]" "$(jqbody -c ".logbook.places[] | select(.id==\"$arc_pin\") | .visits")" \
		"  its visits — every one of them was on the deleted trip, and a pin with none is a WISHLIST place"
	assert_eq 1 "$(in_psql "select count(*) from places where id='$arc_pin'" | tr -d '[:space:]')" \
		"  the row itself — the CRUD reflex deletes it and nothing errors"
	assert_eq 0 "$(in_psql "select count(*) from visits where place_id='$arc_pin'" | tr -d '[:space:]')" \
		"  its rows in visits — visits_trip_fk takes them"
	assert_eq 1 "$(in_psql "select count(*) from cities where id='$arc_city'" | tr -d '[:space:]')" \
		"  the city — nothing in this app authorises destroying one (DEC-57)"
	# THE REPEAT IS A SUCCESS, which is what stops a retried delete taking the
	# client's failure branch (DEC-103) — and it moves no ETag, which is what
	# stops it throwing away the phone's whole cached document.
	local after_delete
	after_delete="$(header ETag)"
	code="$(req -X DELETE "$base/v1/trips/$second_trip" -H "$auth_header")"
	assert_eq 200 "$code" "the SECOND DELETE of the same trip"
	assert_eq "$after_delete" "$(header ETag)" "  its ETag — nothing changed, so nothing moved"

	########################################################################
	# A35-A39: R7's FIVE ROUTES, IN THE RUNNING CONTAINER.
	#
	# THEY RUN AFTER A29, AND THE ORDER WAS DECIDED BY A LEG GOING RED.
	# A29 asserts `walks` comes back as `[]` and not `null` on the write path,
	# and R6 MOVED that claim onto `walks` with the sentence "walks is still
	# untouched by anything in this arc". R7 is the step that stops being true
	# of: this block creates one. Running afterwards keeps A29's leg exactly as
	# R6 wrote it, and it also makes these five routes run against a log that
	# has just been cascaded, which is the harder case rather than the easier
	# one.
	#
	# THE ROWS LIVE ON `$ARC_TRIP` AND AT A PIN OF THEIR OWN, for the second
	# half of the same reason: `photos_trip_fk` and `walks_trip_fk` are CASCADE,
	# so anything on `$second_trip` would be gone by now, and a visit on
	# `$arc_pin` would contradict A29's "every one of them was on the deleted
	# trip".
	########################################################################
	local arc_photo_id="arc-ph-1" arc_walk="arc-walk-1"
	local arc_r7_city="arc-busan" arc_r7_pin="arc-gamcheon"

	step "A35: PUT /v1/photos/$arc_photo_id — a create, and it arrives UNFILED"
	# The city and the pin R7's own steps use, so nothing here disturbs the
	# shape A29 asserts about.
	code="$(req -X PUT "$base/v1/cities/$arc_r7_city" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{name:"Busan",country:{code:"KR",name:"South Korea"},
		                  centre:{lat:35.1796,lng:129.0756}}')")"
	assert_eq 200 "$code" "PUT /v1/cities/$arc_r7_city"
	code="$(req -X PUT "$base/v1/places/$arc_r7_pin" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg c "$arc_r7_city" \
			'{cityId:$c,name:"Gamcheon",coordinates:{lat:35.0975,lng:129.0104}}')")"
	assert_eq 200 "$code" "PUT /v1/places/$arc_r7_pin"

	code="$(req -X PUT "$base/v1/photos/$arc_photo_id" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$ARC_TRIP" --arg c "$arc_r7_city" --arg a "$ARC_DIGEST" \
			'{tripId:$t,cityId:$c,takenAt:"2027-09-28T09:00:00.000Z",asset:$a}')")"
	assert_eq 200 "$code" "PUT /v1/photos/$arc_photo_id"
	assert_eq null "$(jqbody -c .placeId)" "  .placeId — a photograph arrives UNFILED"
	assert_eq null "$(jqbody -c .visitId)" "  .visitId — and both columns or neither"
	assert_contains "$(header ETag)" 'W/"2-' "  the ETag"
	# A BARE Photo IS SAFE AND THIS IS THE MEASUREMENT, on the wire rather than
	# on a Go value: `Photo` carries no list field, so there is no nil slice for
	# the marshaller to write as `null` and no EmitPhoto to add.
	assert_eq 0 "$(jqbody '[to_entries[] | select(.value | type == "array")] | length')" \
		"  list keys in the answer — none, which is why there is no EmitPhoto"

	step "A35b: an UNCOMMITTED asset is refused by name, where photos_asset_fk cannot see it"
	code="$(req -X PUT "$base/v1/photos/arc-ph-2" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$ARC_TRIP" --arg c "$arc_r7_city" --arg a "$ARC_UNCOMMITTED" \
			'{tripId:$t,cityId:$c,takenAt:"2027-09-28T09:01:00.000Z",asset:$a}')")"
	assert_eq 422 "$code" "PUT a photograph naming an object that was begun and never uploaded"
	assert_eq asset "$(jqbody -r .field)" "  the field it names"
	assert_eq 0 "$(in_psql "select count(*) from photos where id='arc-ph-2'" | tr -d '[:space:]')" \
		"  the row after the refusal — an FK cannot see uploaded_at, so this check is Go's"

	step "A36: POST /v1/photos/$arc_photo_id/refile — the occasion the client named"
	# THE MINT PATH, which answers the WHOLE ENVELOPE because two entities
	# moved: the photograph AND the place, whose ordinals were rewritten.
	code="$(req -X POST "$base/v1/photos/$arc_photo_id/refile" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_r7_pin" \
			'{placeId:$p,visitId:"arc-visit-gam-1",visitAt:"2027-09-28T09:00:00.000Z"}')")"
	assert_eq 200 "$code" "refile that OPENS an occasion"
	assert_eq 2 "$(jqbody .version)" "  the format version — this is an ENVELOPE and not a bare photograph"
	assert_eq "arc-visit-gam-1" \
		"$(in_psql "select visit_id from photos where id='$arc_photo_id'" | tr -d '[:space:]')" \
		"  the occasion the client named"
	assert_eq "$arc_r7_pin" \
		"$(in_psql "select place_id from photos where id='$arc_photo_id'" | tr -d '[:space:]')" \
		"  the pin — placeId and visitId move together, always"

	# A SECOND OCCASION AT THE SAME PLACE ON THE SAME TRIP, and then a re-file
	# naming EACH in turn. That is the whole of "the server validates and does
	# not choose": a server picking for itself agrees with the client on ONE of
	# them by luck and cannot agree on both.
	code="$(req -X POST "$base/v1/photos/$arc_photo_id/refile" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_r7_pin" \
			'{placeId:$p,visitId:"arc-visit-gam-2",visitAt:"2027-09-28T19:00:00.000Z"}')")"
	assert_eq 200 "$code" "refile that opens a SECOND occasion the same day"
	for occasion in arc-visit-gam-1 arc-visit-gam-2; do
		code="$(req -X POST "$base/v1/photos/$arc_photo_id/refile" -H "$auth_header" -H "$JSON_CT" \
			-d "$(body_json --arg p "$arc_r7_pin" --arg v "$occasion" '{placeId:$p,visitId:$v}')")"
		assert_eq 200 "$code" "refile to the EXISTING occasion $occasion"
		assert_eq null "$(jqbody -c .logbook)" "  .logbook — absent, because only the photograph moved"
		assert_eq "$occasion" \
			"$(in_psql "select visit_id from photos where id='$arc_photo_id'" | tr -d '[:space:]')" \
			"  the occasion it landed on"
	done
	# THE ORDINALS ARE REWRITTEN IN `at` DESC, which is what the client reads
	# as lastVisited.
	assert_eq "arc-visit-gam-2" \
		"$(in_psql "select id from visits where place_id='$arc_r7_pin' order by ordinal limit 1" | tr -d '[:space:]')" \
		"  ordinal 0 — newest first"

	step "A36b: a re-file that names no occasion is refused, and the server does not pick one"
	code="$(req -X POST "$base/v1/photos/$arc_photo_id/refile" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_r7_pin" '{placeId:$p}')")"
	assert_eq 422 "$code" "refile with no visitId"
	assert_eq visitId "$(jqbody -r .field)" "  the field it names"
	# AND A PLACE IN ANOTHER CITY. $arc_pin is in $arc_city and the photograph
	# was taken in $arc_r7_city.
	code="$(req -X POST "$base/v1/photos/$arc_photo_id/refile" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_pin" '{placeId:$p,visitId:"arc-visit-elsewhere",
		                                     visitAt:"2027-09-28T09:00:00.000Z"}')")"
	assert_eq 422 "$code" "refile to a place in ANOTHER city"
	assert_eq placeId "$(jqbody -r .field)" "  the field it names"
	assert_eq "$arc_r7_pin" \
		"$(in_psql "select place_id from photos where id='$arc_photo_id'" | tr -d '[:space:]')" \
		"  the filing after two refusals — asserted on the ROW, not on the status"

	step "A37: PUT /v1/photos/$arc_photo_id with ONLY a caption — the filing does not move"
	# DEC-89, SAF-MAJ-5. THIS IS THE ONE THE THREE STANDING GUARDS CANNOT SEE:
	# under the whole-state convention this body nulls both columns, and the
	# dangling check, the place-without-occasion query and the pair-agreement
	# assertion all answer zero afterwards.
	local filed_before
	filed_before="$(in_psql "select count(*) from photos where place_id is not null" | tr -d '[:space:]')"
	code="$(req -X PUT "$base/v1/photos/$arc_photo_id" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{caption:"the alleys above the harbour"}')")"
	assert_eq 200 "$code" "PUT with only a caption"
	assert_eq "$arc_r7_pin" "$(jqbody -r .placeId)" "  .placeId in the ANSWER"
	assert_eq "arc-visit-gam-2" "$(jqbody -r .visitId)" "  .visitId in the ANSWER"
	assert_eq "$filed_before" \
		"$(in_psql "select count(*) from photos where place_id is not null" | tr -d '[:space:]')" \
		"  THE COUNT THAT MUST NOT FALL, whole-log, after a caption-only write"
	assert_eq 0 "$(in_psql "select count(*) from photos where (place_id is null) <> (visit_id is null)" | tr -d '[:space:]')" \
		"  half-filed photographs — 0, and it is 0 under the defect too"
	# AND AN EMPTY CAPTION CLEARS THE NOTE RATHER THAN STORING ''.
	code="$(req -X PUT "$base/v1/photos/$arc_photo_id" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{caption:"   "}')")"
	assert_eq 200 "$code" "PUT with a whitespace caption"
	assert_eq null "$(jqbody -c .caption)" "  .caption — NULL and never the empty string"
	assert_eq 1 "$(in_psql "select count(*) from photos where id='$arc_photo_id' and caption is null" | tr -d '[:space:]')" \
		"  the column — photos_caption_present_ck is the guarantee under it"

	step "A38: POST /v1/photos/snooze — three ids, one of them gone, ONE version bump"
	local version_before
	version_before="$(in_psql "select logbook_version from travellers" | tr -d '[:space:]')"
	code="$(req -X POST "$base/v1/photos/snooze" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg p "$arc_photo_id" \
			'{photoIds:[$p,"arc-ph-gone-1","arc-ph-gone-2"],until:"2027-10-19T00:00:00.000Z"}')")"
	assert_eq 200 "$code" "POST /v1/photos/snooze"
	assert_eq 1 "$(jqbody '.photos | length')" "  the rows it wrote — the two unknown ids are SKIPPED"
	assert_eq $((version_before + 1)) "$(in_psql "select logbook_version from travellers" | tr -d '[:space:]')" \
		"  ONE version bump for the whole group"
	assert_eq "$filed_before" \
		"$(in_psql "select count(*) from photos where place_id is not null" | tr -d '[:space:]')" \
		"  the count that must not fall, after a snooze"

	step "A38b: a snooze that matches nothing answers [] and moves no version"
	version_before="$(in_psql "select logbook_version from travellers" | tr -d '[:space:]')"
	code="$(req -X POST "$base/v1/photos/snooze" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{photoIds:["arc-ph-gone-1"],until:"2027-10-19T00:00:00.000Z"}')")"
	assert_eq 200 "$code" "a snooze matching nothing"
	# `jq -c` RENDERS A JSON NULL AS THE FOUR CHARACTERS `null`, so this tells
	# `[]` from `null` — the whole distinction, and the one the client throws on.
	assert_eq "[]" "$(jqbody -c .photos)" "  .photos — [] and NEVER null"
	assert_eq "$version_before" "$(in_psql "select logbook_version from travellers" | tr -d '[:space:]')" \
		"  logbook_version — a write that wrote nothing moves nothing"

	step "A39: PUT /v1/walks/$arc_walk — the track survives both of N1's controls"
	code="$(req -X PUT "$base/v1/walks/$arc_walk" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$ARC_TRIP" --arg c "$arc_r7_city" \
			'{tripId:$t,cityId:$c,recordedOn:"2027-09-28T00:00:00.000Z",distanceKm:6.4,
			  points:[{lat:35.0975,lng:129.0104},{lat:35.0981,lng:129.0117},
			          {lat:35.0996,lng:129.0129}]}')")"
	assert_eq 200 "$code" "PUT /v1/walks/$arc_walk"
	assert_eq 3 "$(jqbody '.points | length')" "  the track it was given"
	assert_eq null "$(jqbody -c .name)" "  .name — a new walk has none, which is what puts it on N1"

	# N1's DISCARD AND N1's 'NAME IT', EACH SENDING WHAT THE CONTROL SENDS.
	# Under the whole-state convention either body writes `points = '[]'`, and
	# `walks_points_array_ck` does not refuse it because an empty array IS an
	# array.
	code="$(req -X PUT "$base/v1/walks/$arc_walk" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{dismissed:true}')")"
	assert_eq 200 "$code" "PUT with ONLY {dismissed:true}"
	assert_eq 3 "$(jqbody '.points | length')" "  .points in the answer — non-zero, never null"
	assert_eq true "$(jqbody .dismissed)" "  .dismissed"
	assert_eq 3 "$(in_psql "select jsonb_array_length(points) from walks where id='$arc_walk'" | tr -d '[:space:]')" \
		"  the COLUMN after N1's Discard — a day that has passed cannot be re-recorded"
	code="$(req -X PUT "$base/v1/walks/$arc_walk" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{name:"Gamcheon and back"}')")"
	assert_eq 200 "$code" "PUT with ONLY {name}"
	assert_eq 3 "$(jqbody '.points | length')" "  .points after a NAME write"
	assert_eq "Gamcheon and back" "$(jqbody -r .name)" "  the name"

	step "A39b: an empty track and an over-long one are both refused NAMING points"
	code="$(req -X PUT "$base/v1/walks/$arc_walk" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{points:[]}')")"
	assert_eq 422 "$code" "PUT with points: []"
	assert_eq points "$(jqbody -r .field)" "  the field it names"
	# 501 POINTS, WELL INSIDE THE 1 MiB BODY CEILING — about 21 KB — so this is
	# ValidateWalk answering and not http.MaxBytesReader, which would carry no
	# field at all (DEC-93, DEC-106).
	code="$(req -X PUT "$base/v1/walks/$arc_walk" -H "$auth_header" -H "$JSON_CT" \
		-d "$(jq -nc '{points: [range(501) | {lat:35.0975123,lng:129.0104567}]}')")"
	assert_eq 422 "$code" "PUT with 501 points"
	assert_eq points "$(jqbody -r .field)" "  the field it names, and NOT a body-too-large"
	code="$(req -X PUT "$base/v1/walks/$arc_walk" -H "$auth_header" -H "$JSON_CT" \
		-d "$(jq -nc '{points: [range(500) | {lat:35.0975123,lng:129.0104567}]}')")"
	assert_eq 200 "$code" "PUT with 500 points — the acceptance half of the pair"
	assert_eq 1 "$(in_psql "select count(*) from walks" | tr -d '[:space:]')" \
		"  walks in the log — a PUT on a client-minted key is idempotent, so six writes to one id are one row"

	########################################################################
	# A41-A46: R8's ONE ROUTE, IN THE RUNNING CONTAINER.
	#
	# THIS IS THE ONLY PLACE `GET /l/{token}` IS EXERCISED WITH NO CREDENTIAL
	# AT ALL, through the real chain, against real PostgreSQL and real MinIO.
	# `go test` can say what the handler answers; only this can say that the
	# capability it embeds FETCHES, that the two cache headers survive the
	# whole middleware chain, and that the token never reaches the container's
	# stdout.
	#
	# THE FIXTURE THE ARC HAS ALREADY BUILT IS WHAT MAKES IT WORTH READING:
	# ARC_TRIP carries a committed cover (A21), a photograph filed to a pin
	# with an occasion (A36), and a walk that N1's 'Discard' was pressed on
	# (A39) — so "a dismissed track is not published" is asserted against a
	# track somebody actually discarded through the API rather than a row
	# somebody set a column on.
	########################################################################
	local public_token="publicshare1" public_wish="arc-wish"

	step "A41: the trip gets a city, a place nobody has been to, a live track and a link"
	# THE CITY, ATTACHED — a public envelope's `cities` is the trip's own
	# itinerary in trip_cities.ordinal order, and ARC_TRIP has had none since
	# A8 asserted `cityIds: []`.
	code="$(req -X PUT "$base/v1/cities/$arc_r7_city" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$ARC_TRIP" '{name:"Busan",attachTo:$t}')")"
	assert_eq 200 "$code" "PUT /v1/cities/$arc_r7_city with attachTo"

	# A WISHLIST PIN IN THE SAME CITY. It is the row PD-07 is about: every key
	# on it is on the allowlist, and it is somewhere the traveller has never
	# been.
	code="$(req -X PUT "$base/v1/places/$public_wish" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg c "$arc_r7_city" \
			'{cityId:$c,name:"Somewhere to go",coordinates:{lat:35.1000,lng:129.0200}}')")"
	assert_eq 200 "$code" "PUT /v1/places/$public_wish"
	assert_eq 0 "$(in_psql "select count(*) from visits where place_id='$public_wish'" | tr -d '[:space:]')" \
		"  visits on the wishlist pin — none, which is what makes it a wishlist pin"

	# A SECOND WALK, NOT DISCARDED. arc-walk-1 is dismissed by now, so without
	# this the `walks` list would be empty for two different reasons at once
	# and neither assertion below would mean anything.
	code="$(req -X PUT "$base/v1/walks/arc-walk-2" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$ARC_TRIP" --arg c "$arc_r7_city" \
			'{tripId:$t,cityId:$c,recordedOn:"2027-09-29T00:00:00.000Z",distanceKm:2.5,
			  points:[{lat:35.0975,lng:129.0104},{lat:35.0981,lng:129.0117}]}')")"
	assert_eq 200 "$code" "PUT /v1/walks/arc-walk-2"
	# `dismissed::text` AND NOT `dismissed`: psql's -tA prints a bare boolean as
	# `t`, which is the same correction A10's `||'|'||` already carries.
	assert_eq true "$(in_psql "select dismissed::text from walks where id='$arc_walk'" | tr -d '[:space:]')" \
		"  arc-walk-1 is still discarded — the row the envelope must not publish"

	code="$(req -X POST "$base/v1/trips/$ARC_TRIP/share" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json --arg t "$public_token" '{token:$t}')")"
	assert_eq 201 "$code" "POST /v1/trips/$ARC_TRIP/share"
	assert_eq true "$(jqbody .shared)" "  shared (DEC-91)"
	assert_eq 0 "$(in_psql "select count(*) from share_links where share_links::text like '%$public_token%'" | tr -d '[:space:]')" \
		"  rows of share_links holding the plaintext (DEC-85)"

	step "A42: GET /l/$public_token — NO Authorization header, and the allowlist"
	# NO -H "$auth_header". That is the whole point of the route and of this
	# line: everything below is what a stranger with a URL can see.
	code="$(req "$base/l/$public_token")"
	assert_eq 200 "$code" "GET /l/{token} with no credential at all"
	# DEC-51's TWO HEADERS, BY PRESENCE (PD-09). This route carries no
	# Authorization header, so RFC 9111 §3.5's shared-cache prohibition — the
	# thing silently protecting GET /v1/logbook — does not reach it.
	assert_eq 'no-store, private' "$(header Cache-Control)" "  Cache-Control"
	assert_eq 'no-referrer' "$(header Referrer-Policy)" "  Referrer-Policy"

	assert_eq "cities photos places trip version walks" \
		"$(jqbody -c 'keys | join(" ")')" "  the six top-level keys"
	assert_eq null "$(jqbody -c '.traveller')" \
		"  .traveller — a link is a capability over ONE TRIP and not over a log"
	assert_eq null "$(jqbody -c '.logbook')" \
		"  .logbook — this is a different shape from the private document"
	assert_eq null "$(jqbody -c '.trip.shareLinkId')" \
		"  .trip.shareLinkId — the capability is not handed back in the body reading it"
	assert_eq null "$(jqbody -c '.trip.sharePhotos')" "  .trip.sharePhotos — the owner's setting"
	assert_eq 2 "$(jqbody .version)" "  the format version"

	# THE ROW RULES, IN THE CONTAINER (PD-07).
	assert_eq 0 "$(jqbody "[.places[] | select(.id==\"$public_wish\")] | length")" \
		"  the wishlist place in the envelope — none. Every key on it is on the allowlist; the ROW is the leak."
	assert_eq 1 "$(jqbody "[.places[] | select(.id==\"$arc_r7_pin\")] | length")" \
		"  the place the trip DID visit — a filter that empties the list satisfies the line above"
	assert_eq 0 "$(jqbody '[.places[] | select(.visits)] | length')" \
		"  places carrying a visits key — none: a Visit carries a tripId and an id, and days is what replaces it"
	# THE DAY COUNT IS READ OFF THE ROWS RATHER THAN WRITTEN AS A LITERAL. What
	# the envelope publishes is this trip's occasions at that pin, and how many
	# the arc has made by now is a fact about the steps above rather than about
	# this one — a literal here is a number somebody edits when A36 changes.
	assert_eq "$(in_psql "select count(*) from visits where place_id='$arc_r7_pin' and trip_id='$ARC_TRIP'" | tr -d '[:space:]')" \
		"$(jqbody '.places[0].days | length')" "  the days on the published place"
	assert_eq 1 "$(jqbody '.walks | length')" "  walks — the discarded one is not published"
	assert_eq arc-walk-2 "$(jqbody -r '.walks[0].id')" "  and it is the one that was not discarded"
	assert_eq null "$(jqbody -c '.walks[0].name')" \
		"  .walks[].name — not on the allowlist: a walk's name is a note in everything but its column"
	assert_eq null "$(jqbody -c '.photos[0].tripId')" \
		"  .photos[].tripId — every photograph here is on the shared trip"

	# THE EMBEDDED CAPABILITY IS REAL, AND IT IS MINTED AT THE PUBLIC LIFETIME
	# (DEC-84). Fifteen minutes rather than two, because this envelope has
	# nothing to re-mint with: POST /v1/media/mint is authenticated.
	local public_cover
	public_cover="$(jqbody -r '.trip.coverUrl')"
	assert_contains "$public_cover" 'X-Amz-Expires=900' \
		"  the cover URL's lifetime — 900s, and 120 would be the phone's own"
	code="$(curl -sS -o "$WORK/fetched" -D /dev/null -w '%{http_code}' "$public_cover")"
	assert_eq 200 "$code" "GET the URL the envelope embedded, with no credential"
	assert_eq "$ARC_PHOTO" "$(cat "$WORK/fetched")" "  the bytes that came back, byte for byte"

	step "A43: shareCoordinates — the city centre is the one that survives"
	# ARC_TRIP's switch is FALSE, which is the client's own default and not an
	# accident: a pin on your accommodation is not something to hand out by
	# link, so it has to be actively turned on every time (A10 asserts it).
	code="$(req "$base/l/$public_token")"
	assert_eq 200 "$code" "GET /l/{token} with coordinates off"
	assert_eq null "$(jqbody -c '.places[0].coordinates')" "  places[].coordinates"
	assert_eq null "$(jqbody -c '.photos[0].coordinates')" "  photos[].coordinates (DEC-108: ONE switch)"
	assert_eq null "$(jqbody -c '.photos[0].accuracyMetres')" "  photos[].accuracyMetres"
	assert_eq 0 "$(jqbody '.walks[0].points | length')" "  walks[].points"
	assert_eq "[]" "$(jqbody -c '.walks[0].points')" "  and it is [] rather than null"
	# THE SCALPEL. `[.. | objects | select(has("lat"))] | length` counts every
	# lat-bearing object at any depth, so this is a claim about the WHOLE
	# document rather than about the fields named above.
	assert_eq 1 "$(jqbody '[.. | objects | select(has("lat"))] | length')" \
		"  lat-bearing objects in the whole document — the ONE city centre, which is what a map opens on"

	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP/share" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{shareCoordinates:true}')")"
	assert_eq 200 "$code" "PUT share {shareCoordinates:true}"
	code="$(req "$base/l/$public_token")"
	assert_eq 200 "$code" "GET /l/{token} with coordinates on"
	assert_eq 2 "$(jqbody '.walks[0].points | length')" "  walks[].points, back"
	assert_contains "$(jqbody -c '.places[0].coordinates')" '"lat"' "  places[].coordinates, back"

	step "A44: sharePhotos off — the array is EMPTY, not missing, and the cover goes"
	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP/share" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{sharePhotos:false}')")"
	assert_eq 200 "$code" "PUT share {sharePhotos:false}"
	code="$(req "$base/l/$public_token")"
	assert_eq 200 "$code" "GET /l/{token} with photographs off"
	assert_eq "[]" "$(jqbody -c '.photos')" \
		"  .photos — an EMPTY ARRAY: a reader that branches on presence has three states to handle"
	assert_eq null "$(jqbody -c '.trip.coverUrl')" "  .trip.coverUrl"
	assert_eq 1 "$(jqbody '.places | length')" "  places — still there, because a pin is not a photograph"
	assert_eq 1 "$(jqbody '.walks | length')" "  walks — still there"
	code="$(req -X PUT "$base/v1/trips/$ARC_TRIP/share" -H "$auth_header" -H "$JSON_CT" \
		-d "$(body_json '{sharePhotos:true}')")"
	assert_eq 200 "$code" "PUT share {sharePhotos:true} — back on for A45"

	step "A45: a REVOKED token and one nobody ever held are one answer (PD-12, DEC-10)"
	local unknown_answer unknown_headers revoked_answer revoked_headers
	code="$(req "$base/l/abcdefghjkmn")"
	assert_eq 404 "$code" "GET /l/{a token nobody ever held}"
	unknown_answer="$(body)"
	unknown_headers="$(tr -d '\r' <"$WORK/head" | grep -viE '^(x-request-id|date):' | sort)"

	code="$(req -X DELETE "$base/v1/trips/$ARC_TRIP/share" -H "$auth_header")"
	assert_eq 200 "$code" "DELETE /v1/trips/$ARC_TRIP/share — H1's 'Stop sharing'"
	assert_eq 1 "$(in_psql "select count(*) from share_links where trip_id='$ARC_TRIP' and revoked_at is not null" | tr -d '[:space:]')" \
		"  the revoked row SURVIVES (DEC-67) — which is what makes the answer below worth asserting"

	code="$(req "$base/l/$public_token")"
	assert_eq 404 "$code" "GET /l/{the revoked token}"
	revoked_answer="$(body)"
	revoked_headers="$(tr -d '\r' <"$WORK/head" | grep -viE '^(x-request-id|date):' | sort)"
	assert_eq "$unknown_answer" "$revoked_answer" \
		"the two bodies — a different one is a plain oracle for which tokens once existed"
	assert_eq "$unknown_headers" "$revoked_headers" "the two header sets, minus X-Request-Id and Date"
	assert_contains "$revoked_answer" 'not_found' \
		"  and it is the vocabulary's word rather than an empty body"

	step "A46: the capability never reached the container's stdout (PD-08)"
	# THE POSITIVE CONTROL FIRST. `grep -c` returning 0 passes against a log
	# that was never written just as well as against a redacted one, and an
	# absence assertion is the easiest kind to write so that it cannot fail.
	local api_log redacted_lines
	api_log="$("${COMPOSE[@]}" logs api 2>&1)"
	redacted_lines="$(printf '%s' "$api_log" | grep -c '/l/\[redacted\]' || true)"
	if [ "$redacted_lines" -lt 1 ]; then
		fail "the api log carries no '/l/[redacted]' at all, so the absence assertion below is measuring nothing"
	fi
	ok "lines carrying /l/[redacted] = $redacted_lines"
	assert_eq 0 "$(printf '%s' "$api_log" | grep -c "$public_token" || true)" \
		"lines carrying the share token itself"

	step "A40: DELETE /v1/photos/$arc_photo_id — 204, a new ETag, and the count falls by one"
	filed_before="$(in_psql "select count(*) from photos where place_id is not null" | tr -d '[:space:]')"
	local visits_before
	visits_before="$(in_psql "select count(*) from visits where place_id='$arc_r7_pin'" | tr -d '[:space:]')"
	code="$(req -X DELETE "$base/v1/photos/$arc_photo_id" -H "$auth_header")"
	assert_eq 204 "$code" "DELETE /v1/photos/$arc_photo_id"
	assert_contains "$(header ETag)" 'W/"2-' "  the ETag on a 204 — the phone has a deletion to stamp"
	assert_eq 0 "$(in_psql "select count(*) from photos where id='$arc_photo_id'" | tr -d '[:space:]')" "  the row"
	assert_eq $((filed_before - 1)) \
		"$(in_psql "select count(*) from photos where place_id is not null" | tr -d '[:space:]')" \
		"  the count that must not fall — a delete lowers it by a KNOWN amount"
	assert_eq "$visits_before" \
		"$(in_psql "select count(*) from visits where place_id='$arc_r7_pin'" | tr -d '[:space:]')" \
		"  the occasions — a photograph does not own the occasion it was filed to"
	# THE REPEAT IS A SUCCESS AND MOVES NOTHING (DEC-103).
	local after_delete
	after_delete="$(header ETag)"
	code="$(req -X DELETE "$base/v1/photos/$arc_photo_id" -H "$auth_header")"
	assert_eq 204 "$code" "the SECOND DELETE of the same photograph"
	assert_eq "$after_delete" "$(header ETag)" "  its ETag — nothing changed, so nothing moved"

	step "A30: DELETE /v1/auth/session — the token stops working, and a typo'd scope is refused"
	# THE TYPO FIRST, because it must change nothing: `?scope=al` signing one
	# device out while the user believes every device is out is the one failure
	# mode this parameter has.
	code="$(req -X DELETE "$base/v1/auth/session?scope=al" -H "$auth_header")"
	assert_eq 422 "$code" "DELETE /v1/auth/session?scope=al"
	assert_eq scope "$(jqbody -r .field)" "  the field it names"
	code="$(req -H "$auth_header" "$base/v1/logbook")"
	assert_eq 200 "$code" "GET /v1/logbook after the refused revocation"

	code="$(req -X DELETE "$base/v1/auth/session" -H "$auth_header")"
	assert_eq 204 "$code" "DELETE /v1/auth/session"
	assert_eq 0 "$(body_bytes)" "bytes in the 204's body"
	code="$(req -H "$auth_header" "$base/v1/logbook")"
	assert_eq 401 "$code" "GET /v1/logbook with the revoked token"
	assert_eq unauthenticated "$(jqbody .code)" "  its code"

	# AND THE ARC GOES ON, so a fresh token is taken. A23 below needs none, but
	# leaving the run holding a dead credential is how a step added later fails
	# for a reason that has nothing to do with it.
	code="$(req -X POST "$base/v1/auth/session" -H "$JSON_CT" \
		-d "$(body_json --arg e "$ARC_EMAIL" --arg p "$ARC_PASS" '{email:$e,passphrase:$p}')")"
	assert_eq 201 "$code" "POST /v1/auth/session, after revoking the last one"
	token="$(jqbody -r .token)"
	auth_header="Authorization: Bearer $token"
	ok "signing in again works — revoking a session is not revoking an account"

	########################################################################
	# A23: `make seed` REFUSES A DATABASE THAT HAS A TRAVELLER (DEC-97).
	#
	# THE ONLY PLACE THE COMMAND ITSELF IS EXERCISED. internal/seed's legs are
	# about FromDocument and Load; this is the binary, its flags, its refusal
	# and its exit code, against a real database in a real stack.
	#
	# AND IT IS WHY THE PLAN'S OWN ACCEPTANCE ORDER CANNOT BE RUN IN ONE
	# PROJECT. DEC-92 reorders the five acceptance checks to
	# `make check && make slice && make seed` so the documented procedure stops
	# teaching "seed, then wipe" — but the arc ENDS with a registered traveller,
	# so a seed against the same project refuses, correctly, every time. The
	# order is right and the two commands belong to two stacks: slice under its
	# own COMPOSE_PROJECT_NAME, seed against the one holding the log.
	#
	# The row count is asserted rather than the exit code alone, because a
	# refusal with a mutation behind it is the failure this is about.
	########################################################################
	step "A23: make seed refuses this database, and changes nothing"
	local trips_before trips_after seed_code
	trips_before="$(in_psql "select count(*) from trips" | tr -d "[:space:]")"
	set +e
	( cd "$REPO" && make seed ) >"$WORK/seed" 2>&1
	seed_code=$?
	set -e
	assert_eq 2 "$seed_code" "make seed's exit code against a database with a traveller"
	assert_contains "$(cat "$WORK/seed")" "already has a traveller" "the refusal"
	assert_contains "$(cat "$WORK/seed")" "$ARC_EMAIL" "the refusal names the traveller it found"
	assert_contains "$(cat "$WORK/seed")" "127.0.0.1" "the refusal names the database it was pointed at"
	trips_after="$(in_psql "select count(*) from trips" | tr -d "[:space:]")"
	assert_eq "$trips_before" "$trips_after" "trips across the refusal"
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
# DEC-92: THE ARC MAY NOT DESTROY THE LIVE VOLUME BY ACCIDENT.
#
# The project name is read from COMPOSE ITSELF rather than from the environment
# variable, for the reason the restart leg's volume name is derived rather than
# written: compose resolves a project name from COMPOSE_PROJECT_NAME, from a
# `name:` key in the file, or from the directory, and only compose knows which
# one won. `deploy/docker-compose.yml` carries `name: travellog`, so the default
# is the live project whether or not anybody set a variable.
#
# THE ESCAPE IS A VARIABLE AND NOT A PROMPT, because this runs unattended; it is
# REQUIRED rather than defaulted, because what is on the other side of the
# refusal is a record with no second copy — `make backup` is the second copy,
# and it is one target away.
########################################################################

LIVE_PROJECT="travellog"

refuse_the_live_project() {
	local project
	project="$("${COMPOSE[@]}" config --format json | jq -r '.name')"
	[ -n "$project" ] && [ "$project" != null ] || fail "compose did not answer for its own project name"

	if [ "$project" != "$LIVE_PROJECT" ]; then
		ok "running under project $project, so ${project}_pgdata is this arc's own volume"
		return 0
	fi
	if [ "${SLICE_DESTROY_VOLUME:-0}" = "1" ]; then
		printf '\033[33m     SLICE_DESTROY_VOLUME=1: destroying %s_pgdata, the LIVE volume\033[0m\n' "$project"
		return 0
	fi

	printf '\n\033[31m    FAIL this arc destroys a named volume, and the project is the live one\033[0m\n' >&2
	printf '\n' >&2
	printf '    A0 runs "docker compose down -v" against project %s, whose volume\n' "$project" >&2
	printf '    %s_pgdata is the one make up, make migrate and make seed write to.\n' "$project" >&2
	printf '    Since R4 that volume holds a record rather than scratch (DEC-92).\n' >&2
	printf '\n' >&2
	printf '    Run it somewhere else, which is one variable:\n' >&2
	printf '\n' >&2
	printf '      COMPOSE_PROJECT_NAME=travellog-slice API_PORT=8085 POSTGRES_PORT=5464 \\\n' >&2
	printf '        MINIO_PORT=9005 S3_PUBLIC_BASE_URL=http://127.0.0.1:9005 make slice\n' >&2
	printf '\n' >&2
	printf '    Or say you mean it:  SLICE_DESTROY_VOLUME=1 make slice\n' >&2
	printf '    Either way, make backup first.\n' >&2
	exit 1
}

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
