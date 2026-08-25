# The gate. There is no CI: `make check` is the only thing standing between a
# change and the repository, and it is manual.
#
# WHY `go vet` IS MANDATORY AND NOT A NICETY: the module's directive is
# `go 1.25.0` written literally (DEC-27), and `go 1.25` was measured to fail
# BOTH build AND vet — x/crypto and x/sys each declare `go 1.25.0`, and the
# directive comparison treats `1.25` as strictly lower. vet is the step that
# enforces the language floor independently of whether the build happened to
# be cached. Dropping it from this target silently removes that enforcement.
#
# `gofmt -l .` is in the chain and MUST PRINT NOTHING, so the target inspects
# its output — a plain `gofmt -l .` in a && chain is a check that cannot fail.
#
# CORRECTION (VS1-FIXES). This comment used to justify inspecting the output by
# saying "gofmt exits 0 whether or not it lists files". That premise holds ONLY
# for input gofmt can parse. On a SYNTAX ERROR gofmt exits **2** and writes to
# **stderr**, so stdout is empty, `[ -n "$out" ]` is false, and the recipe line
# succeeded — `make check` exited **0** with an unparseable .go file in the
# tree. Measured at ee543b9 on a copy: `.tools/broken.go`, a hidden directory
# that `./...` does not match and that internal/config's AST sweep skips, gave
# `gofmt exit=2` and `MAKE EXIT=0`. So the status is now captured as well as
# the output, and both are checked, in that order.
#
# THE LESSON, which is worth more than the fix: VS1 proved this step with ONE
# mutation (a badly formatted but parseable file) and recorded the class as
# closed. That mutation still reddens. A guard proven once against one mutation
# is proven against that mutation, not against its class.

SHELL := /bin/bash

COMPOSE := docker compose -f deploy/docker-compose.yml
COMPOSE_S3 := docker compose -f deploy/docker-compose.test.yml
BIN     := bin/api

# SLICE is a variable so the wiring can be tested without recursion: the arc's
# own record phase runs `make slice SLICE=<stub>` and asserts the exit code
# comes back out. A target that exits 0 having done nothing is indistinguishable
# from one that succeeded (VS1-FIXES finding 7), and that is the class it
# guards.
SLICE   := scripts/slice-arc.sh

.PHONY: build run check fmt up down logs test-db test-s3 migrate slice seed backup

## build — compile the server to bin/api
build:
	go build -trimpath -o $(BIN) ./cmd/api

## run — run the server on the host (not in Docker)
run:
	go run ./cmd/api

## check — THE GATE. build, vet, gofmt, test. Run it before every commit.
check:
	go build ./...
	go vet ./...
	@out="$$(gofmt -l . 2>&1)"; st=$$?; \
	if [ $$st -ne 0 ]; then \
		echo "gofmt itself failed (exit $$st) — this is a file it cannot PARSE,"; \
		echo "not a file it would reformat:"; \
		echo "$$out"; \
		exit $$st; \
	fi; \
	if [ -n "$$out" ]; then \
		echo "gofmt -l reported unformatted files:"; \
		echo "$$out"; \
		echo "run: make fmt"; \
		exit 1; \
	fi
	go test ./...

## fmt — what `make check` tells you to run
fmt:
	gofmt -w .

## up — bring the three-service stack up and wait for every one of them to be healthy
up:
	$(COMPOSE) up -d --build --wait

## down — stop the stack, KEEPING the named volume
down:
	$(COMPOSE) down

## logs — follow the api's output
logs:
	$(COMPOSE) logs -f api

## test-db — the database the internal/postgres tests run against.
## Brings up ONLY postgres, then prints the URL to export. The store tests skip
## without TEST_DATABASE_URL and say so, naming this target.
##
## EVERY FIELD IS DERIVED FROM THE RUNNING CONTAINER, NOT RESTATED.
## This target used to print a hardcoded URL — port 5434, user/password/database
## all literal `travellog` — while compose resolves all four from deploy/.env,
## which .env.example explicitly invites you to change ("it is a fact about one
## machine"). Measured at ee543b9: with deploy/.env setting POSTGRES_PORT=5999,
## POSTGRES_USER=alice, POSTGRES_DB=otherdb, `docker compose config` resolved
## every one of them and this target still printed 127.0.0.1:5434/travellog.
##
## THE CONSEQUENCE IS WORSE THAN A URL THAT WILL NOT CONNECT. On this machine
## 5432 is a developer's own Postgres and 5433 is an unrelated container, so a
## stale printed URL can point host-run internal/postgres tests — which create and
## drop tables — at a database that is not the stack's.
##
## `compose port` answers for the container that is actually running, and the
## three environment values are read out of it, so the print and the stack
## cannot drift apart: there is only one source left.
test-db:
	$(COMPOSE) up -d --wait postgres
	@port="$$($(COMPOSE) port postgres 5432 | sed 's/.*://')"; \
	user="$$($(COMPOSE) exec -T postgres printenv POSTGRES_USER)"; \
	pass="$$($(COMPOSE) exec -T postgres printenv POSTGRES_PASSWORD)"; \
	db="$$($(COMPOSE) exec -T postgres printenv POSTGRES_DB)"; \
	if [ -z "$$port" ] || [ -z "$$user" ] || [ -z "$$db" ]; then \
		echo "make test-db: postgres is up but did not answer for its own" >&2; \
		echo "published port, user or database — refusing to print a URL" >&2; \
		echo "that would be a guess. Try: $(COMPOSE) ps postgres" >&2; \
		exit 1; \
	fi; \
	echo; \
	echo "export TEST_DATABASE_URL=postgres://$$user:$$pass@127.0.0.1:$$port/$$db?sslmode=disable"

## test-s3 — the MinIO the internal/media integration legs run against.
##
## A SECOND STACK, DELIBERATELY. deploy/docker-compose.test.yml has its own
## project name, its own port and its own named volume, so this can run beside
## `make up` and be destroyed without touching the bucket the deployment uses —
## the same separation `make test-db` draws, for the same reason.
##
## EVERY FIELD IS DERIVED FROM THE RUNNING CONTAINER, which is the correction
## `make test-db` already carries: a target that restates a port or a
## credential prints the wrong one the first time somebody overrides it.
##
## The legs are behind a build tag AS WELL AS behind the variable, so `go test
## ./...` never reaches them and `make check` stays four commands:
##
##   TEST_S3_ENDPOINT=... go test -tags integration ./internal/media/ -count=1
test-s3:
	$(COMPOSE_S3) up -d --wait
	@port="$$($(COMPOSE_S3) port minio 9000 | sed 's/.*://')"; \
	user="$$($(COMPOSE_S3) exec -T minio printenv MINIO_ROOT_USER)"; \
	pass="$$($(COMPOSE_S3) exec -T minio printenv MINIO_ROOT_PASSWORD)"; \
	if [ -z "$$port" ] || [ -z "$$user" ] || [ -z "$$pass" ]; then \
		echo "make test-s3: minio is up but did not answer for its own published" >&2; \
		echo "port or credentials — refusing to print values that would be a guess." >&2; \
		echo "Try: $(COMPOSE_S3) ps minio" >&2; \
		exit 1; \
	fi; \
	echo; \
	echo "export TEST_S3_ENDPOINT=http://127.0.0.1:$$port"; \
	echo "export TEST_S3_ACCESS_KEY=$$user"; \
	echo "export TEST_S3_SECRET_KEY=$$pass"

## migrate — apply migrations against the stack's database.
##
## The SERVER ALSO MIGRATES AT BOOT, so this target is not how migrations
## normally run: it exists so a deploy can separate the two, and so the runner
## can be invoked without starting a listener. It runs the SAME binary with
## -migrate-only, inside the compose network, so DATABASE_URL and the other six
## variables come from the compose file rather than from a second copy here —
## the defect `make test-db` had.
migrate:
	$(COMPOSE) up -d --build --wait postgres
	$(COMPOSE) run --rm --no-deps api -migrate-only

## seed — the captured client log into a DEVELOPMENT database (DEC-75).
##
## IT IS NOT PART OF ANY OTHER TARGET AND NOTHING RUNS IT AT BOOT. `make up`
## does not seed, the api image does not link it (cmd/api/imports_test.go is the
## mechanism), and this recipe is the only thing in the repository that calls it.
##
## TWO REFUSALS, AND THEY ARE THE COMMAND'S RATHER THAN THIS FILE'S (DEC-97):
## it refuses when ANY TRAVELLER ROW EXISTS — the same predicate DEC-86 puts on
## register, so `make up && make seed` on a deployed box cannot burn the only
## registration slot the deployment has — and it refuses without the explicit
## dev-database marker below, because a DSN cannot tell a development database
## from a production one. Both refusals print WHERE THEY WERE POINTED.
##
## EVERY VALUE IS DERIVED FROM THE RUNNING CONTAINER, which is the correction
## `make test-db` and `make test-s3` already carry: a recipe that restates a
## port or a credential prints the wrong one the first time somebody overrides
## it, and here "the wrong one" means loading a captured log into a database
## that is not the stack's.
##
## THE PASSPHRASE IS GENERATED PER RUN AND PRINTED ONCE. Nothing stores it.
seed:
	$(COMPOSE) up -d --build --wait
	@port="$$($(COMPOSE) port postgres 5432 | sed 's/.*://')"; \
	user="$$($(COMPOSE) exec -T postgres printenv POSTGRES_USER)"; \
	pass="$$($(COMPOSE) exec -T postgres printenv POSTGRES_PASSWORD)"; \
	db="$$($(COMPOSE) exec -T postgres printenv POSTGRES_DB)"; \
	s3port="$$($(COMPOSE) port minio 9000 | sed 's/.*://')"; \
	s3user="$$($(COMPOSE) exec -T minio printenv MINIO_ROOT_USER)"; \
	s3pass="$$($(COMPOSE) exec -T minio printenv MINIO_ROOT_PASSWORD)"; \
	bucket="$$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' \
		"$$($(COMPOSE) ps -q api)" 2>/dev/null | sed -n 's/^S3_BUCKET=//p')"; \
	if [ -z "$$port" ] || [ -z "$$user" ] || [ -z "$$db" ] || [ -z "$$s3port" ] || [ -z "$$bucket" ]; then \
		echo "make seed: the stack is up but did not answer for its own published" >&2; \
		echo "port, credentials or bucket — refusing to aim a loader at a guess." >&2; \
		echo "  postgres $$port  minio $$s3port  bucket '$$bucket'" >&2; \
		exit 1; \
	fi; \
	go run ./cmd/seed \
		-dsn "postgres://$$user:$$pass@127.0.0.1:$$port/$$db?sslmode=disable" \
		--i-know-this-is-a-dev-database \
		-s3-endpoint "http://127.0.0.1:$$s3port" \
		-s3-bucket "$$bucket" \
		-s3-access-key "$$s3user" \
		-s3-secret-key "$$s3pass"

## backup — a custom-format pg_dump of the stack's database, keeping 7 (DEC-92).
##
## R4 IS WHERE THE VOLUME STOPS BEING DISPOSABLE, and this is the target that
## says so. The plan's own premise is that PostgreSQL is the record and the
## phone is a cache; before this, `pg_dump` had zero hits across the whole plan
## and `make slice`'s first step destroyed the volume the record lives in.
##
## `-Fc` AND NOT PLAIN SQL. Custom format is what `pg_restore` reads, and
## pg_restore is version-tolerant in the direction that matters: a dump taken by
## pg_dump 17 restores into 17 or later. A plain-SQL dump is also a single
## `psql -f` with no way to restore one table, no compression, and no way to
## list what is in it without reading all of it.
##
## THE DUMP IS TAKEN INSIDE THE CONTAINER, so the client and the server are the
## same build by construction. A host pg_dump older than the server refuses
## outright ("server version mismatch"), which is a failure mode that only shows
## up on somebody else's machine.
##
## KEEP 7. Not a retention policy — a floor under "I broke it on Tuesday".
## docs/BEFORE-A-PUBLIC-DEPLOY.md carries what this is NOT: it is not off-box,
## it is not scheduled, and it does not include the BUCKET. A database restore
## without a bucket restore is a log every reference of which resolves, pointing
## at nothing (DEF-07).
##
## AND WHEN THE BUCKET IS BACKED UP, THE ORDER IS FIXED: bucket first, database
## second. A dump newer than the bucket copy references objects that were never
## copied; a bucket copy newer than the dump only leaves unreferenced garbage.
backup:
	@mkdir -p backups
	$(COMPOSE) up -d --wait postgres
	@user="$$($(COMPOSE) exec -T postgres printenv POSTGRES_USER)"; \
	db="$$($(COMPOSE) exec -T postgres printenv POSTGRES_DB)"; \
	if [ -z "$$user" ] || [ -z "$$db" ]; then \
		echo "make backup: postgres is up but did not answer for its own user or" >&2; \
		echo "database — refusing to dump from a guess." >&2; \
		exit 1; \
	fi; \
	stamp="$$(date -u +%Y%m%dT%H%M%SZ)"; \
	out="backups/travellog-$$stamp.dump"; \
	$(COMPOSE) exec -T postgres pg_dump -Fc -U "$$user" "$$db" > "$$out"; \
	if [ ! -s "$$out" ]; then \
		echo "make backup: $$out is empty — pg_dump wrote nothing. Removing it," >&2; \
		echo "because an empty file in backups/ is worse than no file." >&2; \
		rm -f "$$out"; exit 1; \
	fi; \
	echo "wrote $$out ($$(wc -c < "$$out" | tr -d ' ') bytes)"; \
	ls -1t backups/travellog-*.dump 2>/dev/null | tail -n +8 | while read -r old; do \
		echo "rotating out $$old"; rm -f "$$old"; \
	done; \
	echo "kept: $$(ls -1 backups/travellog-*.dump 2>/dev/null | wc -l | tr -d ' ') of 7"

## slice — the whole arc against the live stack, from cold, plus the four
## standing legs that need something `make check` deliberately does not have.
##
## IT DESTROYS THE NAMED VOLUME OF WHATEVER PROJECT IT RUNS UNDER. `docker
## compose down -v` is its first step, on purpose: a 201 against a database that
## already held the row proves nothing.
##
## SO IT REFUSES THE DEFAULT PROJECT (DEC-92, and it is the whole reason this
## guard exists). Before R4 the volume held nothing anybody would miss and the
## only protection was a paragraph in this file; R4 is the step that puts a
## record in it, and five of the eight steps' acceptance checks read
## `make seed && make check && make slice` — a documented procedure teaching a
## developer that seeding and then wiping is normal. The arc already ran two of
## its five phases under their own COMPOSE_PROJECT_NAME, so the main phase using
## the live project was the inconsistency rather than the pattern.
##
## Run it somewhere else, which is one variable:
##
##   COMPOSE_PROJECT_NAME=travellog-slice API_PORT=8085 POSTGRES_PORT=5464 \
##     MINIO_PORT=9005 S3_PUBLIC_BASE_URL=http://127.0.0.1:9005 make slice
##
## SLICE_DESTROY_VOLUME=1 is the way to say "yes, destroy the live one". It is a
## variable and not a prompt because the arc has to run unattended, and it is
## required rather than defaulted because the failure it prevents is somebody
## else's photographs.
##
## NOT PART OF `make check`, for the reason `test-image` is not: the gate is
## four commands and stays fast (4.4s measured at d7a6da9), and this builds an
## image and brings three compose projects up.
##
## `scripts/slice-arc.sh record` / `gate` / `arc` / `testdb` / `healthcheck`
## runs one phase; no argument runs all five.
slice:
	@$(SLICE)

## test-image — the opt-in image tier (test/image). Everything about the
## shipped artefact that `go test ./...` cannot reach: what the scratch image
## contains, who it runs as, whether Docker's own HEALTHCHECK goes healthy,
## and whether the named volume survives `down` then `up`.
##
## DELIBERATELY NOT PART OF `make check`. The gate is four commands and stays
## fast; this builds an image and brings two compose projects up, on their own
## ports and under their own project names so it can run beside `make up`.
## Without Docker, or without the variable, every leg SKIPS and says so.
.PHONY: test-image
test-image:
	TRAVELLOG_IMAGE_TESTS=1 go test -v -count=1 -timeout 30m ./test/image/
