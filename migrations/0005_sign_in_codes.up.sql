-- 0005 — sign-in codes, because the passphrase is going.
--
-- WHY THIS IS NOT AN EDIT TO 0001, for the fourth time and for the reason
-- 0002, 0003 and 0004 all give: `loadMigrations` checksums sha256 over the
-- whole file, comments included, so any edit to an applied migration raises
-- ErrChecksumMismatch and the process refuses to boot.
--
-- ONE ROW PER TRAVELLER, AND THE PRIMARY KEY IS THE WHOLE SECURITY ARGUMENT.
-- A six-digit code is a million possibilities and survives five wrong
-- guesses, which is a bound only while a traveller can hold ONE live code. If
-- requesting a code appended a row, an attacker would request two hundred and
-- hold a thousand valid guesses — and each of those codes would be as good as
-- the traveller's own. `PRIMARY KEY (traveller_id)` is what makes a request
-- REPLACE rather than accumulate, and it is in the schema rather than in the
-- service because a rule the service owns is a rule a second caller forgets.
--
-- IT ALSO REMOVES THE SWEEP. One row per traveller means the table is bounded
-- by the traveller count whatever happens, so an expired code costs a row and
-- not a growing table, and nothing here needs a cron.
--
-- THE DIGEST IS NOT WHAT PROTECTS A CODE, and `internal/auth/code.go` says so
-- at length rather than letting the column imply otherwise: a million
-- possibilities is exhausted in under a second by anyone holding this table.
-- What protects a code is `expires_at`, the burn on use, and `attempts`. The
-- digest keeps the code out of a backup and out of a log, and the traveller
-- salt in the Go side means the table an attacker builds is per account.
--
-- ATTEMPTS ARE COUNTED HERE AND NOT IN THE LIMITER. `internal/httpx`'s
-- limiter is an in-memory map keyed on client address, so it is per process
-- and per address — an attacker rotating addresses is not slowed by it, and
-- a second API instance would double it silently. Five guesses against a
-- million is safe; five guesses per address is not a bound at all. This
-- column is the bound.
--
-- ON DELETE CASCADE, because account deletion is immediate and total, and a
-- live sign-in code is exactly the sort of thing that is forgotten by a
-- hand-written delete and remembered by a foreign key.
--
-- ENTIRELY TRANSACTIONAL: one CREATE TABLE and its constraints, none of which
-- PostgreSQL refuses inside a transaction block.

CREATE TABLE sign_in_codes (
	traveller_id uuid        NOT NULL,
	code_hash    bytea       NOT NULL,
	issued_at    timestamptz NOT NULL DEFAULT now(),
	expires_at   timestamptz NOT NULL,
	attempts     integer     NOT NULL DEFAULT 0,

	CONSTRAINT sign_in_codes_pkey PRIMARY KEY (traveller_id),

	CONSTRAINT sign_in_codes_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,

	-- The same CHECK `sessions` and `share_links` carry, for the reason their
	-- comments give: a truncated digest inserts cleanly and compares without
	-- complaint, so nothing downstream can tell it from a whole one.
	CONSTRAINT sign_in_codes_code_hash_sha256_ck
		CHECK (octet_length(code_hash) = 32),

	-- A negative attempt count would make the cap unreachable, and the only
	-- way to get one is a decrement nobody meant to write.
	CONSTRAINT sign_in_codes_attempts_ck CHECK (attempts >= 0),

	-- An already-expired code is a row nothing can use, issued by a caller
	-- that has got its clock or its TTL wrong. Refusing it here is how that
	-- is found at the write rather than at the sign-in that fails later.
	CONSTRAINT sign_in_codes_expiry_after_issue_ck CHECK (expires_at > issued_at)
);
