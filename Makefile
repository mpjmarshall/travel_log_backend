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
# `gofmt -l .` is in the chain and MUST PRINT NOTHING. gofmt exits 0 whether or
# not it lists files, so the target inspects its output rather than its status;
# a plain `gofmt -l .` in a && chain is a check that cannot fail.

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
	@out="$$(gofmt -l .)"; \
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
test-db:
	$(COMPOSE) up -d --wait postgres
	@echo
	@echo "export TEST_DATABASE_URL=postgres://travellog:travellog@127.0.0.1:5434/travellog?sslmode=disable"

## migrate — apply migrations against DATABASE_URL.
## NOT IMPLEMENTED UNTIL VS4, and it fails loudly rather than exiting 0 on
## nothing: a no-op migrate target is indistinguishable from a successful one.
migrate:
	@echo "make migrate: the migration runner lands in VS4 (internal/store/migrate.go)." >&2
	@echo "Nothing to apply yet, and exiting 0 here would be a lie." >&2
	@exit 1

## slice — the whole arc against the live stack, from cold.
## NOT IMPLEMENTED UNTIL VS8 (scripts/slice-arc.sh), for the same reason.
slice:
	@echo "make slice: scripts/slice-arc.sh lands in VS8." >&2
	@echo "Its restart leg is the only proof of the named volume, so it cannot be faked." >&2
	@exit 1
