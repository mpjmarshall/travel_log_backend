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

.PHONY: build run check fmt up down logs test-db test-s3 migrate slice

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

## slice — the whole arc against the live stack, from cold, plus the four
## standing legs that need something `make check` deliberately does not have.
##
## IT DESTROYS THE NAMED VOLUME. `docker compose down -v` is its first step, on
## purpose: a 201 against a database that already held the row proves nothing.
## Run it against the local stack and expect to lose whatever is in it.
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
