-- travellog 0001 — the entities, and the cascades the delete sheets specify.
--
-- POSTGRESQL 15 IS A HARD FLOOR (DEC-66), and it is the column-list form of
-- ON DELETE SET NULL that puts it there. Measured on 17.11: a composite FK's
-- plain `ON DELETE SET NULL` nulls EVERY column of the referencing key,
-- traveller_id included, and traveller_id is NOT NULL. PostgreSQL echoes its
-- own generated statement — `UPDATE ONLY "public"."photos" SET
-- "traveller_id" = NULL, "place_id" = NULL` — and the delete ABORTS. D2's keep
-- branch and `_repointed`, two of the seven cascades this file implements, are
-- both unexecutable without the column list. Three review passes missed it:
-- the trip-delete path never reaches the broken FK, because the photograph is
-- cascade-deleted through photos.trip_id first.
--
-- EVERY CONSTRAINT IS NAMED. PostgreSQL's generated names are built from the
-- COLUMN names — `photos_traveller_id_place_id_fkey` — so a rename or a re-add
-- in a later migration moves them, and the catalog legs then redden for a
-- reason that has nothing to do with the schema being wrong. Named constraints
-- also make the string a user eventually sees in a 409/422 stable.
--
-- IDS ARE SLUGS AND NEVER char(n) (DEC-02): blank padding makes 'kyoto     '
-- compare equal to 'kyoto'. The alphabet is permissive because the ids are the
-- client's own and must round-trip; all 43 fixture ids are 1–64 of [a-z0-9-].
--
-- EACH FOREIGN KEY BELOW IS EITHER A SENTENCE FROM A DELETE SHEET OR THE
-- ABSENCE OF ONE. The sheet line is quoted above the table it constrains.

-- travellers.
--
-- DEC-65 SUPERSEDES DEC-60: an address's uniqueness is enforced by the
-- database, not by two call sites remembering to lowercase. THE PLAIN UNIQUE ON
-- THE COLUMN IS DELIBERATELY ABSENT — keeping both means a case variant fails
-- on whichever constraint the planner reaches first, with the wrong message.
-- The address is stored AS TYPED: the local part of an email is case-sensitive
-- per RFC 5321, and the index makes lowercasing on write unnecessary.
--
-- LOOKUPS MUST BE `WHERE lower(email) = lower($1)`. Measured on 17.11:
-- `WHERE email = $1` is a Seq Scan that does not use this index and returns
-- ZERO rows against a differently-cased stored address, so a forgotten call
-- site does not error — it reports an unknown address and sign-in fails with a
-- correct-looking response.
--
-- IT IS A FUNCTIONAL INDEX, so `lower` resolves through search_path. pg_catalog
-- is implicitly first unless something names it explicitly later, and the
-- runner pins search_path for exactly this reason. Measured: under
-- `SET search_path = <schema>, pg_catalog` with a shadowing `lower` defined,
-- both sides of the comparison collapse to one constant and the predicate
-- matches EVERY row — an address nobody registered resolves to a traveller,
-- with no error anywhere.
CREATE TABLE travellers (
	id              uuid        NOT NULL,
	email           text        NOT NULL,
	passphrase_hash text        NOT NULL,
	name            text,
	logbook_version bigint      NOT NULL DEFAULT 0,
	created_at      timestamptz NOT NULL DEFAULT now(),

	CONSTRAINT travellers_pkey PRIMARY KEY (id),
	CONSTRAINT travellers_email_present_ck CHECK (email <> ''),
	CONSTRAINT travellers_passphrase_hash_present_ck CHECK (passphrase_hash <> ''),
	CONSTRAINT travellers_name_present_ck CHECK (name IS NULL OR name <> ''),
	CONSTRAINT travellers_logbook_version_ck CHECK (logbook_version >= 0)
);

CREATE UNIQUE INDEX travellers_email_lower_key ON travellers (lower(email));

-- sessions.
--
-- token_hash is SHA-256 and the CHECK says so in bytes: a one-byte token_hash
-- inserted successfully before the constraint existed.
CREATE TABLE sessions (
	id           uuid        NOT NULL,
	traveller_id uuid        NOT NULL,
	token_hash   bytea       NOT NULL,
	created_at   timestamptz NOT NULL DEFAULT now(),
	last_used_at timestamptz NOT NULL DEFAULT now(),
	expires_at   timestamptz NOT NULL,
	revoked_at   timestamptz,

	CONSTRAINT sessions_pkey PRIMARY KEY (id),
	CONSTRAINT sessions_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT sessions_token_hash_sha256_ck CHECK (octet_length(token_hash) = 32),
	CONSTRAINT sessions_expires_after_created_ck CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
CREATE INDEX sessions_traveller_idx ON sessions (traveller_id);

-- media_objects.
--
-- The bucket key is `<traveller_uuid>/<sha256>` (DEC-38), so the row is keyed
-- (traveller_id, id) and the bucket must be too — or one traveller's sweep
-- deletes another's objects the day a second traveller can register.
-- media_objects_id_sha256_ck is what pins the encoding to hex rather than
-- base64 (DEC-38's v5 amendment).
--
-- media_objects_content_type_present_ck IS THE WEAKEST CHECK IN THIS FILE and
-- is deliberately marked so: DEC-51's content-type allowlist is named nowhere,
-- so the constraint that belongs here cannot be written. It stops '' and
-- nothing else. `content_type = 'text/html; <script>'` is accepted. The
-- allowlist lands with the media routes.
CREATE TABLE media_objects (
	traveller_id uuid        NOT NULL,
	id           text        NOT NULL,
	byte_size    bigint      NOT NULL,
	content_type text        NOT NULL,
	created_at   timestamptz NOT NULL DEFAULT now(),
	uploaded_at  timestamptz,

	CONSTRAINT media_objects_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT media_objects_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT media_objects_id_sha256_ck CHECK (id ~ '^[0-9a-f]{64}$'),
	CONSTRAINT media_objects_byte_size_ck CHECK (byte_size > 0),
	CONSTRAINT media_objects_content_type_present_ck CHECK (content_type <> '')
);

-- cities.
--
-- DEC-59: country stays flattened into two columns. The client has no country
-- concept — no input, no screen, no list — and derives it from the city.
--
-- cities_cover_fk is DEC-58: a real foreign key rather than a Go check, because
-- a Go check can be bypassed by the next route somebody adds and nothing
-- notices. It is NULLABLE, and a composite FK is MATCH SIMPLE by default, so a
-- NULL cover_asset is satisfied without any parent lookup and nothing is forced
-- to carry a cover. DO NOT MAKE IT MATCH FULL: that rejects exactly that case.
--
-- cities_country_code_ck exists because `country_code = 'JAPAN'` — five
-- characters — inserted successfully before it did.
CREATE TABLE cities (
	traveller_id uuid             NOT NULL,
	id           text             NOT NULL,
	name         text             NOT NULL,
	country_code text             NOT NULL,
	country_name text             NOT NULL,
	centre_lat   double precision NOT NULL,
	centre_lng   double precision NOT NULL,
	cover_asset  text,
	geocoder_ref text,
	created_at   timestamptz      NOT NULL DEFAULT now(),

	CONSTRAINT cities_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT cities_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT cities_cover_fk FOREIGN KEY (traveller_id, cover_asset)
		REFERENCES media_objects (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT cities_id_length_ck CHECK (length(id) BETWEEN 1 AND 64),
	CONSTRAINT cities_id_charset_ck CHECK (id ~ '^[a-z0-9-]+$'),
	CONSTRAINT cities_name_present_ck CHECK (name <> ''),
	CONSTRAINT cities_country_code_ck CHECK (country_code ~ '^[A-Z]{2}$'),
	CONSTRAINT cities_country_name_present_ck CHECK (country_name <> ''),
	CONSTRAINT cities_centre_lat_ck CHECK (centre_lat BETWEEN -90 AND 90),
	CONSTRAINT cities_centre_lng_ck CHECK (centre_lng BETWEEN -180 AND 180),
	CONSTRAINT cities_cover_asset_sha256_ck CHECK (cover_asset IS NULL OR cover_asset ~ '^[0-9a-f]{64}$')
);

CREATE INDEX cities_cover_idx ON cities (traveller_id, cover_asset);

-- trips.
--
-- DEC-69: `started_on` and `ended_on`, NOT `start` and `end`. `end` is a
-- RESERVED keyword — `SELECT id, start, end FROM trips` is a syntax error — and
-- in a project with no ORM every hand-written query would have to remember the
-- double quotes forever. THE WIRE KEYS STAY `start` AND `end`; the emitter maps
-- them, so nothing in the client or the approved diagrams changes.
--
-- DEC-64: `city_ids jsonb` IS GONE. See trip_cities below.
--
-- Both dates may be absent — T4's "Add dates" is a control the user may never
-- press — but a trip ending before it starts inserted successfully before
-- trips_dates_ordered_ck existed.
CREATE TABLE trips (
	traveller_id      uuid        NOT NULL,
	id                text        NOT NULL,
	name              text        NOT NULL,
	started_on        date,
	ended_on          date,
	summary           text,
	cover_asset       text,
	share_photos      boolean     NOT NULL DEFAULT false,
	share_notes       boolean     NOT NULL DEFAULT false,
	share_coordinates boolean     NOT NULL DEFAULT false,
	created_at        timestamptz NOT NULL DEFAULT now(),

	CONSTRAINT trips_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT trips_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT trips_cover_fk FOREIGN KEY (traveller_id, cover_asset)
		REFERENCES media_objects (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT trips_id_length_ck CHECK (length(id) BETWEEN 1 AND 64),
	CONSTRAINT trips_id_charset_ck CHECK (id ~ '^[a-z0-9-]+$'),
	CONSTRAINT trips_name_present_ck CHECK (name <> ''),
	CONSTRAINT trips_dates_ordered_ck CHECK (ended_on IS NULL OR started_on IS NULL OR ended_on >= started_on),
	CONSTRAINT trips_cover_asset_sha256_ck CHECK (cover_asset IS NULL OR cover_asset ~ '^[0-9a-f]{64}$')
);

CREATE INDEX trips_cover_idx ON trips (traveller_id, cover_asset);

-- trip_cities — DEC-64, which reverses DEC-26's jsonb.
--
-- A join table needs no Postgres array support at all: every column is a plain
-- scalar. So jsonb bought nothing and cost referential integrity plus the
-- reverse lookup (which trips include this city), which P1's year rows lean on.
-- ORDERING IS LOAD-BEARING — cityIds is an ordered list in travel order, so
-- emit `ORDER BY ordinal`, never natural order.
--
-- trip_cities_trip_fk IS CASCADE AND trip_cities_city_fk IS RESTRICT, and the
-- asymmetry is read off the sheets. D3 destroys a trip and says so; nothing in
-- the app authorises destroying a city, and DEC-69 corrects DEC-64 here:
-- DEC-64 claimed CASCADE gave DEC-57's city RESTRICT "a real child to protect",
-- and executed it did the opposite — a city with no places, photographs or
-- walks deleted silently and vanished from every trip's ordered list, leaving a
-- gap in the ordinals.
--
-- trip_cities_traveller_fk IS WHAT MAKES ACCOUNT DELETION POSSIBLE, and a test
-- found it rather than a reading. Without it `DELETE FROM travellers` fails
-- with `update or delete on table "cities" violates foreign key constraint
-- "trip_cities_city_fk"`. The mechanism is the AFTER-trigger queue: deleting a
-- traveller queues one cascade per FK that references travellers DIRECTLY, all
-- in one batch, and each cascade APPENDS the checks its own delete provokes.
-- The cascade into cities appends the RESTRICT check for trip_cities.city_id,
-- while the rows that check looks for are removed by the cascade from trips —
-- appended after it. Every other entity table already had a direct traveller
-- cascade and is emptied in the first batch, which is why trip_cities was the
-- only table with the problem and why it was invisible before DEC-64.
--
-- THE MANDATED WRITE STRATEGY FOR A REORDER IS DELETE-THEN-INSERT, and that is
-- a measurement. The non-deferrable UNIQUE below does not collide that way:
-- DELETE and INSERT are separate statements and the DELETE completes first.
-- What DOES collide is UPDATE-in-place, including the set-based
-- `UPDATE ... SET ordinal = 1 - ordinal`, because a UNIQUE index is checked per
-- ROW during a statement even when the final state is unique. DEFERRABLE
-- INITIALLY DEFERRED fixes the UPDATE form and moves the violation to COMMIT,
-- which is a worse shape for a hand-rolled database/sql handler — the 422 would
-- have to be mapped off an error returned by tx.Commit(). Live trap:
-- `SET CONSTRAINTS ALL DEFERRED` against a NON-deferrable constraint succeeds
-- silently and changes nothing.
CREATE TABLE trip_cities (
	traveller_id uuid    NOT NULL,
	trip_id      text    NOT NULL,
	city_id      text    NOT NULL,
	ordinal      integer NOT NULL,

	CONSTRAINT trip_cities_pkey PRIMARY KEY (traveller_id, trip_id, city_id),
	CONSTRAINT trip_cities_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT trip_cities_trip_fk FOREIGN KEY (traveller_id, trip_id)
		REFERENCES trips (traveller_id, id) ON DELETE CASCADE,
	CONSTRAINT trip_cities_city_fk FOREIGN KEY (traveller_id, city_id)
		REFERENCES cities (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT trip_cities_ordinal_uq UNIQUE (traveller_id, trip_id, ordinal),
	CONSTRAINT trip_cities_ordinal_ck CHECK (ordinal >= 0)
);

-- DEC-70. city_id is the THIRD column of the primary key, so the primary key
-- cannot serve the foreign key to cities: only an index's LEADING columns can.
-- DEC-64 introduced a new unindexed FK child column in the same breath as the
-- ruling written to eliminate that exact class.
CREATE INDEX trip_cities_city_idx ON trip_cities (traveller_id, city_id);

-- places.
--
-- places_city_fk IS THE FIRST OF THE THREE CITY FOREIGN KEYS THAT ARE NOT READ
-- OFF A SHEET, and that is the point (DEC-57). The client has no delete-a-city
-- control, so no user-facing sentence authorises a cascade — and a cascade here
-- would be the largest destructive act in the app: every place in the city,
-- every photograph taken there across every trip, every walk. RESTRICT rather
-- than NO ACTION because it checks immediately and states the intent. Whoever
-- adds a delete-a-city control is stopped by the database exactly when they
-- should be writing the sheet copy. RESTRICT -> CASCADE is a one-line
-- migration; CASCADE -> RESTRICT after it has destroyed photographs is not a
-- migration at all.
CREATE TABLE places (
	traveller_id uuid             NOT NULL,
	id           text             NOT NULL,
	city_id      text             NOT NULL,
	name         text             NOT NULL,
	lat          double precision NOT NULL,
	lng          double precision NOT NULL,
	plan         text,
	cover_asset  text,
	created_at   timestamptz      NOT NULL DEFAULT now(),

	CONSTRAINT places_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT places_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT places_city_fk FOREIGN KEY (traveller_id, city_id)
		REFERENCES cities (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT places_cover_fk FOREIGN KEY (traveller_id, cover_asset)
		REFERENCES media_objects (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT places_id_length_ck CHECK (length(id) BETWEEN 1 AND 64),
	CONSTRAINT places_id_charset_ck CHECK (id ~ '^[a-z0-9-]+$'),
	CONSTRAINT places_name_present_ck CHECK (name <> ''),
	CONSTRAINT places_lat_ck CHECK (lat BETWEEN -90 AND 90),
	CONSTRAINT places_lng_ck CHECK (lng BETWEEN -180 AND 180),
	CONSTRAINT places_cover_asset_sha256_ck CHECK (cover_asset IS NULL OR cover_asset ~ '^[0-9a-f]{64}$')
);

CREATE INDEX places_city_idx ON places (traveller_id, city_id);
CREATE INDEX places_cover_idx ON places (traveller_id, cover_asset);

-- visits.
--
-- DEC-68: `at` is `timestamptz`, NOT `date`. The wire carries a real time of
-- day — the reference log holds "2027-12-06T07:05:00.000Z" — and `date`
-- truncates it silently. Two losses, both invisible: DEC-26's refile ordinal
-- rule cannot break a tie between two visits on the same day, which is exactly
-- the Kyoto-in-May shape the fixture is built around; and a date-only string
-- makes the client's `DateTime.parse` return a NON-UTC local time, unlike every
-- other date in the log. trips.started_on, trips.ended_on and walks.recorded_on
-- stay `date` because those genuinely are midnight-UTC on the wire — but THE
-- EMITTER MUST RE-RENDER THEM as T00:00:00.000Z or the same parse asymmetry
-- appears there instead.
--
-- visits_place_fk is D2's "the visits go with the pin"; visits_trip_fk is D3,
-- and the places survive it.
--
-- visits_place_ordinal_uq GUARDS THE ORDINAL THAT ACTUALLY REBINDS
-- PHOTOGRAPHS, which was unguarded while the cosmetic one on trip_cities had a
-- constraint. Two visits of one place could both hold ordinal 0, at which point
-- `ORDER BY ordinal` is non-deterministic — and DEC-26 says what that costs:
-- emit the visits in a different order and a photograph silently rebinds to a
-- different occasion. Read them `ORDER BY ordinal, id` so a pre-existing
-- duplicate degrades to stable rather than random. It also SUBSUMES DEC-63's
-- index on (traveller_id, place_id): those are its leading two columns, so a
-- separate index would be a duplicate.
CREATE TABLE visits (
	traveller_id uuid        NOT NULL,
	id           text        NOT NULL,
	place_id     text        NOT NULL,
	trip_id      text        NOT NULL,
	ordinal      integer     NOT NULL,
	at           timestamptz NOT NULL,
	note         text,
	created_at   timestamptz NOT NULL DEFAULT now(),

	CONSTRAINT visits_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT visits_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT visits_place_fk FOREIGN KEY (traveller_id, place_id)
		REFERENCES places (traveller_id, id) ON DELETE CASCADE,
	CONSTRAINT visits_trip_fk FOREIGN KEY (traveller_id, trip_id)
		REFERENCES trips (traveller_id, id) ON DELETE CASCADE,
	CONSTRAINT visits_place_ordinal_uq UNIQUE (traveller_id, place_id, ordinal),
	CONSTRAINT visits_id_length_ck CHECK (length(id) BETWEEN 1 AND 64),
	CONSTRAINT visits_id_charset_ck CHECK (id ~ '^[a-z0-9-]+$'),
	CONSTRAINT visits_ordinal_ck CHECK (ordinal >= 0)
);

CREATE INDEX visits_trip_idx ON visits (traveller_id, trip_id);

-- photos.
--
-- photos_trip_fk is D3's "N photos and their notes". photos_city_fk is DEC-57
-- again, as on places.
--
-- photos_place_fk IS D2's KEEP BRANCH — "they lose the pin but keep their date
-- and city" — and THE COLUMN LIST IS WHAT MAKES IT EXECUTABLE; see the header.
-- D2's DELETE branch is statement ORDER in Go and not a foreign key: the
-- photographs must be deleted BEFORE the place, or this clears their place_id
-- and the delete then matches nothing.
--
-- photos_visit_fk is `_repointed`: deleting a trip clears visit_id on ANOTHER
-- trip's photograph and clears nothing else. Without it the log holds
-- photographs naming a visit that has gone, and no count moves.
--
-- photos_asset_fk is DEC-58's one NOT NULL media reference. A photograph
-- without its asset is not a photograph.
--
-- photos_coordinates_paired_ck exists because the client's field is a single
-- nullable LatLng, which cannot represent half a coordinate — and a photograph
-- with lat set and lng NULL inserted successfully before the constraint did.
CREATE TABLE photos (
	traveller_id    uuid             NOT NULL,
	id              text             NOT NULL,
	trip_id         text             NOT NULL,
	city_id         text             NOT NULL,
	place_id        text,
	visit_id        text,
	taken_at        timestamptz      NOT NULL,
	asset           text             NOT NULL,
	caption         text,
	lat             double precision,
	lng             double precision,
	accuracy_metres integer,
	filed_later     timestamptz,
	created_at      timestamptz      NOT NULL DEFAULT now(),

	CONSTRAINT photos_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT photos_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT photos_trip_fk FOREIGN KEY (traveller_id, trip_id)
		REFERENCES trips (traveller_id, id) ON DELETE CASCADE,
	CONSTRAINT photos_city_fk FOREIGN KEY (traveller_id, city_id)
		REFERENCES cities (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT photos_place_fk FOREIGN KEY (traveller_id, place_id)
		REFERENCES places (traveller_id, id) ON DELETE SET NULL (place_id),
	CONSTRAINT photos_visit_fk FOREIGN KEY (traveller_id, visit_id)
		REFERENCES visits (traveller_id, id) ON DELETE SET NULL (visit_id),
	CONSTRAINT photos_asset_fk FOREIGN KEY (traveller_id, asset)
		REFERENCES media_objects (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT photos_id_length_ck CHECK (length(id) BETWEEN 1 AND 64),
	CONSTRAINT photos_id_charset_ck CHECK (id ~ '^[a-z0-9-]+$'),
	CONSTRAINT photos_asset_sha256_ck CHECK (asset ~ '^[0-9a-f]{64}$'),
	CONSTRAINT photos_lat_ck CHECK (lat IS NULL OR lat BETWEEN -90 AND 90),
	CONSTRAINT photos_lng_ck CHECK (lng IS NULL OR lng BETWEEN -180 AND 180),
	CONSTRAINT photos_coordinates_paired_ck CHECK ((lat IS NULL) = (lng IS NULL)),
	CONSTRAINT photos_accuracy_metres_ck CHECK (accuracy_metres IS NULL OR accuracy_metres >= 0)
);

CREATE INDEX photos_trip_idx ON photos (traveller_id, trip_id);
CREATE INDEX photos_city_idx ON photos (traveller_id, city_id);
CREATE INDEX photos_place_idx ON photos (traveller_id, place_id);
CREATE INDEX photos_visit_idx ON photos (traveller_id, visit_id);
CREATE INDEX photos_asset_idx ON photos (traveller_id, asset);

-- walks.
--
-- walks_trip_fk is D3's "N recorded walks"; walks_city_fk is DEC-57 a third
-- time. THERE IS NO place_id ON THIS TABLE, and that absence is D2's "the track
-- stays with the day it was recorded either way" — removePlace must not touch
-- walks.
--
-- walks_points_array_ck exists because `points = '"not an array"'` is valid
-- jsonb and inserted successfully before the constraint did.
CREATE TABLE walks (
	traveller_id uuid             NOT NULL,
	id           text             NOT NULL,
	trip_id      text             NOT NULL,
	city_id      text             NOT NULL,
	recorded_on  date             NOT NULL,
	distance_km  double precision NOT NULL,
	points       jsonb            NOT NULL,
	name         text,
	dismissed    boolean          NOT NULL DEFAULT false,
	created_at   timestamptz      NOT NULL DEFAULT now(),

	CONSTRAINT walks_pkey PRIMARY KEY (traveller_id, id),
	CONSTRAINT walks_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT walks_trip_fk FOREIGN KEY (traveller_id, trip_id)
		REFERENCES trips (traveller_id, id) ON DELETE CASCADE,
	CONSTRAINT walks_city_fk FOREIGN KEY (traveller_id, city_id)
		REFERENCES cities (traveller_id, id) ON DELETE RESTRICT,
	CONSTRAINT walks_id_length_ck CHECK (length(id) BETWEEN 1 AND 64),
	CONSTRAINT walks_id_charset_ck CHECK (id ~ '^[a-z0-9-]+$'),
	CONSTRAINT walks_distance_km_ck CHECK (distance_km >= 0),
	CONSTRAINT walks_points_array_ck CHECK (jsonb_typeof(points) = 'array'),
	CONSTRAINT walks_name_present_ck CHECK (name IS NULL OR name <> '')
);

CREATE INDEX walks_trip_idx ON walks (traveller_id, trip_id);
CREATE INDEX walks_city_idx ON walks (traveller_id, city_id);

-- share_links — DEC-67.
--
-- The primary key was never specified, and the two candidates behave
-- differently for a control the app has. With PRIMARY KEY (traveller_id,
-- trip_id) — the natural reading of "1 to 0..1" — H1's "Stop sharing" followed
-- by "New link" FAILS OUTRIGHT: executed, `duplicate key value violates unique
-- constraint "share_links_pkey"`. History is kept instead, which matters
-- because DEC-10 stores the token in plaintext: the record now shows which
-- token was live when. "New link" is: revoke the current row, insert a new one,
-- same transaction.
--
-- share_links_one_live IS THE ONLY THING ENFORCING "0..1 LIVE", which the class
-- diagram already claims and which neither candidate primary key delivers.
--
-- share_links_token_key is global rather than per traveller because the public
-- read is GET /l/{token}, which arrives with no traveller in hand.
--
-- share_links_trip_idx: DEC-70 SAID TO DROP THIS AS A DUPLICATE OF THE PRIMARY
-- KEY, AND THAT INSTRUCTION IS STALE. It was measured against a reconstruction
-- whose PK was (traveller_id, trip_id); DEC-67, ruled in the same batch, moved
-- the PK to (traveller_id, token), so (traveller_id, trip_id) leads no index.
-- share_links_one_live cannot serve the foreign key either — it is PARTIAL, and
-- an RI check needs an index covering every row. The catalog leg derived from
-- pg_index.indkey is what caught it, which is DEC-70's own mechanism catching
-- DEC-70's own instruction.
CREATE TABLE share_links (
	traveller_id uuid        NOT NULL,
	trip_id      text        NOT NULL,
	token        text        NOT NULL,
	created_at   timestamptz NOT NULL DEFAULT now(),
	revoked_at   timestamptz,

	CONSTRAINT share_links_pkey PRIMARY KEY (traveller_id, token),
	CONSTRAINT share_links_traveller_fk FOREIGN KEY (traveller_id)
		REFERENCES travellers (id) ON DELETE CASCADE,
	CONSTRAINT share_links_trip_fk FOREIGN KEY (traveller_id, trip_id)
		REFERENCES trips (traveller_id, id) ON DELETE CASCADE,
	CONSTRAINT share_links_token_present_ck CHECK (token <> ''),
	CONSTRAINT share_links_revoked_after_created_ck CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE UNIQUE INDEX share_links_token_key ON share_links (token);

CREATE UNIQUE INDEX share_links_one_live ON share_links (traveller_id, trip_id)
	WHERE revoked_at IS NULL;

CREATE INDEX share_links_trip_idx ON share_links (traveller_id, trip_id);
