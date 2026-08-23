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
BIN     := bin/api

.PHONY: build run check fmt up down logs test-db migrate slice

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

## up — bring the two-service stack up and wait for the api to be healthy
up:
	$(COMPOSE) up -d --build --wait

## down — stop the stack, KEEPING the named volume
down:
	$(COMPOSE) down

## logs — follow the api's output
logs:
	$(COMPOSE) logs -f api

## test-db — the database the internal/store tests run against.
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
## stale printed URL can point host-run internal/store tests — which create and
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

## slice — the whole arc against the live stack, from cold.
## NOT IMPLEMENTED UNTIL VS8 (scripts/slice-arc.sh), for the same reason.
slice:
	@echo "make slice: scripts/slice-arc.sh lands in VS8." >&2
	@echo "Its restart leg is the only proof of the named volume, so it cannot be faked." >&2
	@exit 1

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
