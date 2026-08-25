// What a city write and a place write may contain, and the ONE contract in
// this API where "absent" and "empty" are two different destructive answers.
//
// THE TWO ENTITIES ARE IN ONE FILE BECAUSE THEIR WRITES ARE ONE CONTRACT.
// `PlaceWrite.CityID` names a city, `CityWrite.AttachTo` names a trip, and
// `places_city_fk` is `ON DELETE RESTRICT` (DEC-57) — so the order a client
// creates them in is fixed and the refusals reference each other. Splitting
// them would put half of one sentence in each of two files.
//
// EVERY FIELD IS A POINTER, BECAUSE ABSENT MEANS LEAVE ALONE (DEC-89), and on
// `visits` that ruling is not a convenience — it is the difference between a
// no-op and thirty photographs losing the occasion they were taken on.
//
//	MEASURED against the client's own fixture on postgres:17.11, running
//	R6's OWN MANDATED SHAPE with an EMPTY incoming array at `fushimi-inari`:
//
//	  UPDATE visits SET ordinal = ordinal + 1000 WHERE place_id='fushimi-inari'
//	  DELETE FROM visits WHERE place_id='fushimi-inari' AND id <> ALL('{}')
//	  -> UPDATE 28 / DELETE 28
//
//	  visits at the place        28 -> 0
//	  photographs still filed    30 -> 0        across THREE trips
//	  photographs naming a place with no occasion   0 -> 30
//	  the dangling-reference check                  0 -> 0
//
//	Whole-log the same operation unfiles 95 photographs and destroys 5 visit
//	notes, and every table count except `visits` is unchanged.
//
// THE DANGLING CHECK ANSWERING 0 THROUGHOUT IS THE POINT. The reference is
// GONE, not dangling, so R5's `expectNoDanglingReferences` cannot see it, the
// place-without-occasion query sees no place, and a pair-agreement check sees
// a pair that agrees — both columns are NULL and therefore agree. What sees it
// is a count that must not fall: `SELECT count(*) FROM photos WHERE place_id
// IS NOT NULL` (DEC-89, SAF-MAJ-5).
//
// SO THE CONTRACT IS THREE ANSWERS AND NOT TWO:
//
//	visits ABSENT   leave every visit exactly where it is. This is what makes
//	                createPlace correct by construction — C1's pin is "a place
//	                on the city's wishlist" with no visits at all, and "a place
//	                with no visits IS a wishlist place" is the client's own
//	                sentence — and it is what makes an accidental re-PUT
//	                harmless.
//	visits: []      an explicit request to clear. NO CLIENT CONTROL ISSUES IT,
//	                so it is REFUSED with a 422 naming the field until one
//	                does. "A sheet describing a choice the model cannot offer
//	                is worse than a sheet offering less" cuts both ways: a
//	                route offering a destruction no sheet authorises is the
//	                same error.
//	visits: [...]   the whole ordered array, NEWEST FIRST, ordinals assigned
//	                from position. The write is an UPSERT and never a
//	                delete-then-insert — see internal/postgres/place_store.go,
//	                which is where that costs something.
package logbook

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxNoteBytes bounds the two free-text fields this step introduces —
// `places.plan` and `visits.note` — and it is THIS BUILD'S POLICY rather than
// schema, in exactly the sense MaxNameBytes and MaxSummaryBytes are.
//
// Both columns are `text` and nothing in 0001 bounds either, so without this a
// one-megabyte plan is storable and then re-emitted on every read of the whole
// log, for ever. It is the same number as MaxSummaryBytes because it is the
// same kind of field — a paragraph somebody typed — and it is a separate
// constant so that moving one does not move the other by accident.
const MaxNoteBytes = 4096

// countryCodePattern is spec L23's compiled regexp for DEC-59's flattened
// country, and it is the same expression as `cities_country_code_ck`.
//
// IT IS HERE AS WELL AS THERE FOR THE REASON assetPattern IS: a CHECK
// violation reaches the client as a 500 with no field on it, and the schema is
// still what enforces it. 0001's own comment records why the check exists at
// all — `country_code = 'JAPAN'`, five characters, inserted successfully
// before it did.
var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)

// CityWrite is the body of `PUT /v1/cities/{id}`: T5's 'Add a city', which in
// the client is `createCity` AND `setTripCities` and here is one request.
//
// THE POINTERS ARE SINGLE ON FOUR FIELDS AND DOUBLE ON ONE, and which is which
// is read off the schema rather than chosen. `name`, `country_code`,
// `country_name`, `centre_lat` and `centre_lng` are all NOT NULL, so there is
// no sent-as-null state for them to carry and `*T` says everything there is to
// say. `cover_asset` is nullable, so it gets TripWrite's `**T` — with the same
// measured caveat that the third state is reachable from Go and not over the
// wire (see TripWrite).
//
// COUNTRY COMES FROM THE REQUEST AND IS NOT DERIVED (DEC-59). The client's
// geocoder supplies it and the user never types it: T5 has a city field and no
// country input anywhere, and there is no countries table for the server to
// look one up in. A server that guessed would be inventing an entity nothing
// asks for.
//
// `attachTo` IS NOT A COLUMN AND THAT IS WHY IT IS LAST. It names the trip the
// new city joins, and honouring it appends the id to that trip's `cityIds` IN
// THE SAME TRANSACTION — so the route becomes CASCADING and answers the whole
// logbook rather than the city. The client's own `createCity` takes exactly
// this argument and appends at the END of the ordered list
// (`logbook.dart:341`: `t.withCities([...t.cityIds, id])`), which is the
// ordering asserted rather than assumed.
type CityWrite struct {
	ID         *string  `json:"id"`
	Name       *string  `json:"name"`
	Country    *Country `json:"country"`
	Centre     *LatLng  `json:"centre"`
	CoverAsset **string `json:"coverAsset"`
	AttachTo   *string  `json:"attachTo"`
}

// ValidateCity answers the first field that is wrong, and nothing about
// whether the ids it names exist.
//
// EXISTENCE IS THE STORE'S, under the traveller's advisory lock, for the
// reason ValidateTrip gives: a check made out here is a check made against a
// database that can move underneath it. So `attachTo` is refused here for not
// being the SHAPE of an id and there for not being a trip in this log.
//
// AND "A CITY NEEDS A NAME" IS NOT HERE EITHER. Absent is legal on an UPDATE
// and impossible on a CREATE, and only the store knows which it is holding —
// the same half-answerable rule TripWrite has, and it is enforced beside the
// existence checks.
func ValidateCity(c CityWrite) error {
	if c.ID == nil || !idPattern.MatchString(*c.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if c.Name != nil {
		if err := checkName(*c.Name, "name", "a city needs a name"); err != nil {
			return err
		}
	}
	if c.Country != nil {
		if !countryCodePattern.MatchString(c.Country.Code) {
			return InvalidFieldError{Field: "country",
				Why: fmt.Sprintf("%q is not an ISO-3166-1 alpha-2 code: two capitals",
					c.Country.Code)}
		}
		if strings.TrimSpace(c.Country.Name) == "" {
			return InvalidFieldError{Field: "country",
				Why: "a country code arrives with the name the geocoder gave it"}
		}
	}
	if c.Centre != nil {
		if err := checkLatLng(*c.Centre, "centre"); err != nil {
			return err
		}
	}
	if cover := Value(c.CoverAsset); cover != nil && !assetPattern.MatchString(*cover) {
		return InvalidFieldError{Field: "coverAsset",
			Why: "a cover is a media object id: 64 lowercase hex characters"}
	}
	if c.AttachTo != nil && !idPattern.MatchString(*c.AttachTo) {
		return InvalidFieldError{Field: "attachTo",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	return nil
}

// PlaceWrite is the body of `PUT /v1/places/{id}`: C1's pin, and the only
// route in this API that carries a whole ordered child collection.
//
// `Visits` IS A POINTER TO A SLICE AND THE INDIRECTION IS THE WHOLE FEATURE.
// A bare `[]Visit` makes absent and empty the same value — which is precisely
// the defect DEC-89 was ruled about, measured on `TripWrite.CityIDs` against a
// running server — and here the two mean opposite things. See the file
// comment for the three answers and the numbers behind them.
//
// THE ELEMENTS ARE WHOLE `Visit`s AND NOT A SECOND WRITE TYPE. A visits array
// is not partial: the client reads the array, reorders or appends, and sends
// it back, so every element already carries every field the row has. A
// `VisitWrite` with five pointers would be five more absent-means-leave-alone
// questions about a value the client always has in hand — and it would make
// the round trip in `internal/seed` unable to feed this route the document it
// just read.
type PlaceWrite struct {
	ID          *string  `json:"id"`
	CityID      *string  `json:"cityId"`
	Name        *string  `json:"name"`
	Coordinates *LatLng  `json:"coordinates"`
	Visits      *[]Visit `json:"visits"`
	Plan        **string `json:"plan"`
	CoverAsset  **string `json:"coverAsset"`
}

// ValidatePlace answers the first field that is wrong.
//
// THE `visits: []` REFUSAL IS HERE AND NOT IN THE STORE, and that placement is
// the decision. It is a fact about the REQUEST — "this build has no control
// that clears an occasion list, so it will not act on one" — rather than a
// fact about any row, so it can be answered before a transaction is opened and
// before the traveller's advisory lock is taken. A refusal that needs no
// database is a refusal that cannot be reached by a slow path.
func ValidatePlace(p PlaceWrite) error {
	if p.ID == nil || !idPattern.MatchString(*p.ID) {
		return InvalidFieldError{Field: "id",
			Why: "an id is 1 to 64 characters of a-z, 0-9 and hyphen"}
	}
	if p.CityID != nil && !idPattern.MatchString(*p.CityID) {
		return InvalidFieldError{Field: "cityId",
			Why: fmt.Sprintf("%q is not an id", *p.CityID)}
	}
	if p.Name != nil {
		if err := checkName(*p.Name, "name", "a place needs a name"); err != nil {
			return err
		}
	}
	if p.Coordinates != nil {
		if err := checkLatLng(*p.Coordinates, "coordinates"); err != nil {
			return err
		}
	}
	if plan := Value(p.Plan); plan != nil && len(*plan) > MaxNoteBytes {
		return InvalidFieldError{Field: "plan",
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(*plan), MaxNoteBytes)}
	}
	if cover := Value(p.CoverAsset); cover != nil && !assetPattern.MatchString(*cover) {
		return InvalidFieldError{Field: "coverAsset",
			Why: "a cover is a media object id: 64 lowercase hex characters"}
	}
	if p.Visits != nil {
		if err := checkVisits(*p.Visits, *p.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkVisits is the field the whole step is about.
//
// THE EMPTY-ARRAY BRANCH IS FIRST, and it is the one that had to be written
// down rather than derived. PD-06's upsert fix closes a no-op re-send of an
// UNCHANGED array and does NOT close this one: the mandated shape ends "DELETE
// only the ids absent from the incoming array", and when the array is empty
// every id is absent, so it does exactly what delete-then-insert did with the
// fix in place. The numbers are in the file comment.
func checkVisits(visits []Visit, placeID string) error {
	if len(visits) == 0 {
		return InvalidFieldError{Field: "visits",
			Why: "an empty visits array is a request to clear every occasion at this " +
				"place, which unfiles every photograph filed to it — no control in the " +
				"client asks for that, so this build refuses it. OMIT the key to leave " +
				"the visits alone"}
	}
	seen := make(map[string]bool, len(visits))
	for _, visit := range visits {
		if !idPattern.MatchString(visit.ID) {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("%q is not a visit id", visit.ID)}
		}
		if seen[visit.ID] {
			// visits_pkey is (traveller_id, id), so a repeated id makes the
			// upsert's own multi-row INSERT collide with itself — which
			// PostgreSQL answers with "ON CONFLICT DO UPDATE command cannot
			// affect row a second time", a sentence with no field on it.
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("%q appears twice, and an occasion happens once", visit.ID)}
		}
		seen[visit.ID] = true

		if !idPattern.MatchString(visit.TripID) {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("the visit %s names %q, which is not a trip id",
					visit.ID, visit.TripID)}
		}
		// THE PLACE IS THE PATH'S AND A DISAGREEING BODY IS REFUSED, which is
		// the rule `putTrip` already applies to its own id. A visit naming
		// another place inside this place's array is a client that believes it
		// is writing somewhere else, and honouring either half of that would
		// move an occasion nobody asked to move. An EMPTY placeId is the
		// ordinary case for a client that lets the path carry it.
		if visit.PlaceID != "" && visit.PlaceID != placeID {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("the visit %s names the place %q and this write is to %q",
					visit.ID, visit.PlaceID, placeID)}
		}
		if visit.Note != nil && len(*visit.Note) > MaxNoteBytes {
			return InvalidFieldError{Field: "visits",
				Why: fmt.Sprintf("the note on %s is %d bytes, and this build takes at most %d",
					visit.ID, len(*visit.Note), MaxNoteBytes)}
		}
	}
	return nil
}

func checkName(name, field, why string) error {
	if strings.TrimSpace(name) == "" {
		return InvalidFieldError{Field: field, Why: why}
	}
	if len(name) > MaxNameBytes {
		return InvalidFieldError{Field: field,
			Why: fmt.Sprintf("%d bytes, and this build takes at most %d", len(name), MaxNameBytes)}
	}
	return nil
}

// checkLatLng is the Go half of `cities_centre_lat_ck` and `places_lat_ck`.
// The CHECK is still what enforces it; this is what names the field first.
func checkLatLng(at LatLng, field string) error {
	if at.Lat < -90 || at.Lat > 90 {
		return InvalidFieldError{Field: field,
			Why: fmt.Sprintf("a latitude is between -90 and 90, and %v is not", at.Lat)}
	}
	if at.Lng < -180 || at.Lng > 180 {
		return InvalidFieldError{Field: field,
			Why: fmt.Sprintf("a longitude is between -180 and 180, and %v is not", at.Lng)}
	}
	return nil
}

// PhotoDisposition is D2's two branches, and IT HAS NO USABLE ZERO VALUE ON
// PURPOSE (SAF-MAJ-4's near-miss, and the reason the parameter is required).
//
// `?photos=keep|delete` is REQUIRED and not defaulted: "a default is a silent
// answer to the question D2 makes the user answer on screen", and the sheet
// makes them answer it because the two branches destroy different amounts. A
// `bool` would have made `false` mean keep, so a caller that forgot the
// question would get one of the two answers and no error — which is the same
// class of defect as `[]Visit` making absent and empty one value.
//
// SO THE ZERO VALUE IS `photosUnspecified` AND IT IS UNEXPORTED. Nothing
// outside this package can name it, and `Service.RemovePlace` refuses it
// before the store is reached, so "there is no default" is a property of the
// type rather than a rule a handler has to remember.
type PhotoDisposition int

const (
	photosUnspecified PhotoDisposition = iota

	// KeepPhotos is D2's "they lose the pin but keep their date and city" —
	// the client's own `Photo.copyWith(clearPlace: true)`.
	KeepPhotos

	// DeletePhotos is D2's "all N, and the notes you wrote on them".
	DeletePhotos
)

func (d PhotoDisposition) String() string {
	switch d {
	case KeepPhotos:
		return "keep"
	case DeletePhotos:
		return "delete"
	default:
		return "unspecified"
	}
}

// ParsePhotoDisposition reads the query parameter, and an ABSENT one is
// refused by the same branch a misspelled one is.
//
// BOTH ARE `InvalidFieldError{Field: "photos"}` AND THE SAMENESS IS THE POINT:
// to a caller they are one condition — this route will not guess how far a
// deletion reaches — and giving them two different sentences would suggest one
// of them has a safe answer.
func ParsePhotoDisposition(raw string) (PhotoDisposition, error) {
	switch raw {
	case "keep":
		return KeepPhotos, nil
	case "delete":
		return DeletePhotos, nil
	default:
		return photosUnspecified, InvalidFieldError{Field: "photos",
			Why: "removing a place asks what happens to the photographs filed there: " +
				"?photos=keep leaves them with their date and city, ?photos=delete " +
				"takes them and the notes written on them. There is no default"}
	}
}
