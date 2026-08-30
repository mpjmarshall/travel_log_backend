-- 0006 — invite codes, replacing the rule that registration closes after the
-- first traveller.
--
-- Not an edit to an applied migration, for the reason 0002 to 0005 all give:
-- loadMigrations checksums the whole file and a changed one refuses to boot.
--
-- WHY THE ONE-TRAVELLER RULE GOES. It was a land-grab guard: on a deployed
-- instance a stranger who registered first got an authenticated account with a
-- 600/min budget. An invite is the same guard without the product limit, so
-- the deployment can hold more than one person.
--
-- THE CLAIM MUST BE ATOMIC OR THE CODE IS NOT SINGLE USE. Read-then-write lets
-- two registrations arriving together both see an unused row and both proceed,
-- which is one invite admitting two people. used_at is what an UPDATE ... WHERE
-- used_at IS NULL tests and sets in one statement.
--
-- THE PLAINTEXT IS NOT STORED, on the same terms as a share token: an invite
-- is a capability, and a dump that holds every live one in the clear is a dump
-- that admits anybody who reads it.
--
-- Entirely transactional: one CREATE TABLE and its constraints.

CREATE TABLE invite_codes (
	code_hash  bytea       NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	note       text,
	used_at    timestamptz,
	used_by    uuid,

	CONSTRAINT invite_codes_pkey PRIMARY KEY (code_hash),

	CONSTRAINT invite_codes_code_hash_sha256_ck
		CHECK (octet_length(code_hash) = 32),

	-- used_at ALONE IS THE SPENT MARKER, and there is deliberately no check
	-- pairing it with used_by. Pairing them makes account deletion fail: the
	-- foreign key below nulls used_by and the check would then reject a row
	-- whose used_at is still set. A deleted traveller must not hand their
	-- invite back, so used_at is the one that must never be cleared.
	--
	-- used_by is who claimed it, and it may become null when they leave.
	CONSTRAINT invite_codes_used_by_fk FOREIGN KEY (used_by)
		REFERENCES travellers (id) ON DELETE SET NULL (used_by)
);

-- The foreign key's child column set is (used_by), and the primary key leads
-- with code_hash, so nothing indexes it. Without this, deleting a traveller
-- scans every invite ever minted.
CREATE INDEX invite_codes_used_by_idx ON invite_codes (used_by);
