// The public envelope: what `GET /l/{token}` answers, key for key.
//
// IT IS AN ALLOWLIST AND NOT A SET OF THINGS TO HIDE, and that is the whole
// design. docs/PUBLIC-ENVELOPE.md §1 is the specification these types are
// written against, and it was written from the three sharing flags and the
// schema rather than from this handler — an allowlist derived from a handler
// is a description of what the handler happens to emit, which is the one thing
// an allowlist must not be.
//
// SO THESE ARE SEPARATE TYPES AND NOT THE DOCUMENT'S OWN WITH TAGS ADDED.
// `omitempty`, a second set of tags or a marshaller that drops fields would
// all make the public shape a FUNCTION of the private one: a field added to
// `Photo` next month would be published by default, and the only thing
// standing between a stranger and it would be whoever remembered. A field
// added to `Photo` cannot reach a `PublicPhoto` at all — it has to be typed
// out here, in a file whose whole subject is what a stranger may see.
//
// WHAT THIS FILE DOES NOT DECIDE: WHICH ROWS. docs/PUBLIC-ENVELOPE.md §5 has
// three row rules — which places, which of a place's visits, and which
// photographs and walks — and none of them is here. They are the STORE's, in
// SQL (internal/postgres/share_read.go), because they are claims about which
// rows exist rather than about which keys survive. A second implementation
// here would be a second thing to keep in agreement, and PD-07's leak was
// exactly a disagreement of that kind.
//
// THE READER IS NOBODY. `/l/{token}` carries no Authorization header, so the
// reader cannot mint a media capability — `POST /v1/media/mint` is
// authenticated. Every picture the envelope shows is therefore EMBEDDED as a
// signed URL, minted at `S3_PRESIGN_TTL_PUBLIC` (15 minutes, DEC-84), and that
// fifteen minutes is A HARD WALL RATHER THAN A ROLLING WINDOW: it runs from the
// moment the envelope was generated and there is no refresh path an
// unauthenticated reader can take. Whoever renders a share page must re-GET
// `/l/{token}` to refresh them.
package logbook

// Public is the whole answer: exactly six keys.
//
// `traveller` IS NOT ONE OF THEM. The owner's name is not part of a trip, and
// a share link is a capability over ONE TRIP rather than over a log. This is
// the single largest difference from the private document and it is the
// easiest to reintroduce by copying the emitter.
//
// `logbook` IS NOT ONE OF THEM EITHER. The private read nests its five lists
// under `{"version":…,"logbook":{…}}`; this is a different shape — one trip, no
// traveller, URLs where object ids were — so nesting it under the same key
// would invite a reader to decode it with the logbook codec and get a document
// whose `trips` list is missing.
type Public struct {
	Version int           `json:"version"`
	Trip    PublicTrip    `json:"trip"`
	Cities  []PublicCity  `json:"cities"`
	Places  []PublicPlace `json:"places"`
	Photos  []PublicPhoto `json:"photos"`
	Walks   []PublicWalk  `json:"walks"`
}

// PublicTrip is seven keys.
//
// `coverUrl` REPLACES `coverAsset`: the private document carries a 64-hex
// object id the phone mints a URL for, and the public reader cannot mint.
//
// `shareLinkId`, `sharePhotos`, `shareNotes`, `shareCoordinates` and `shared`
// are NOT here. They are the owner's settings, and the reader's business is
// what they can see rather than how it was decided. `shareLinkId` in
// particular would hand the reader the capability back inside the body it is
// already reading — and after DEC-85 the server does not hold the plaintext to
// emit anyway.
type PublicTrip struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	CityIDs  []string `json:"cityIds"`
	Start    *Instant `json:"start"`
	End      *Instant `json:"end"`
	Summary  *string  `json:"summary"`
	CoverURL *string  `json:"coverUrl"`
}

// PublicCity is four keys, and `centre` is the one coordinate that survives
// `shareCoordinates` being false — see EmitPublic.
//
// `coverAsset`/`coverUrl` is NOT here. A city's cover is chrome and the page
// has the trip's; leaving it out is one fewer minted capability per request.
type PublicCity struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Country Country `json:"country"`
	Centre  LatLng  `json:"centre"`
}

// PublicPlace is five keys, and `days` is the one rename in this document.
//
// `visits` IS NOT HERE AND `days` IS WHAT REPLACES IT. A `Visit` carries a
// `tripId` and an `id`, and publishing a place's visits whole is precisely how
// another trip's history leaks. The rename has a cost — a reader written
// against the private document has to learn one more name — and the cost is
// what it buys: `visits` in the private document means EVERY visit of that
// place, and `visits` here would mean THIS TRIP'S visits only, which is the
// same word for two different sets. A distinct name is what stops somebody
// restoring `tripId` and `id` "for symmetry" and reopening the leak in a
// change that looks like tidying.
//
// `plan` IS NOT HERE. A plan is a note-to-self about a place you have not been
// to yet; it is not part of a trip that happened.
type PublicPlace struct {
	ID          string      `json:"id"`
	CityID      string      `json:"cityId"`
	Name        string      `json:"name"`
	Coordinates *LatLng     `json:"coordinates"`
	Days        []PublicDay `json:"days"`
}

// PublicDay is what a Visit becomes: two keys.
//
// Only the visits ON THE SHARED TRIP reach here, so `tripId` has nothing left
// to say and `id` names a row no reader can address.
type PublicDay struct {
	At   Instant `json:"at"`
	Note *string `json:"note"`
}

// PublicPhoto is eight keys.
//
// `url` REPLACES `asset`, minted at the public TTL.
//
// `tripId` IS NOT HERE: every photograph in this document is on the shared
// trip, and a key whose value is the same on every row only tells a reader
// what they already know. `visitId` IS NOT HERE either — it names a row that
// is not in this document under that name.
type PublicPhoto struct {
	ID             string  `json:"id"`
	CityID         string  `json:"cityId"`
	PlaceID        *string `json:"placeId"`
	TakenAt        Instant `json:"takenAt"`
	URL            string  `json:"url"`
	Caption        *string `json:"caption"`
	Coordinates    *LatLng `json:"coordinates"`
	AccuracyMetres *int    `json:"accuracyMetres"`
}

// PublicWalk is five keys.
//
// `name` IS NOT HERE: a walk's name is written by N1's 'Name it' and is a note
// in everything but the column it lives in. `dismissed` IS NOT HERE because a
// dismissed walk is not published at all — which is a ROW rule and is the
// store's.
type PublicWalk struct {
	ID         string   `json:"id"`
	CityID     string   `json:"cityId"`
	RecordedOn Instant  `json:"recordedOn"`
	DistanceKm float64  `json:"distanceKm"`
	Points     []LatLng `json:"points"`
}

// PublicSource is what the store hands over: the rows that are already allowed
// to be here, in the domain's own types.
//
// IT REUSES Trip, City, Place, Photo AND Walk RATHER THAN DECLARING FIVE MORE
// TYPES, and the reason is the opposite of the one that makes the Public types
// separate. These are what came OUT OF THE DATABASE — the store scans them the
// way every other read does — and the filtering happens on the way to the
// public types, in one function, where it can be read. A parallel set of
// "row" types would be five more shapes to keep in step with the schema for no
// decision.
//
// `Trip` CARRIES THE THREE FLAGS AND THAT IS WHY IT IS THE WHOLE TYPE. They
// decide everything below, and passing them separately would let a caller hand
// this function a trip and somebody else's switches.
//
// `Places[].Visits` IS ALREADY FILTERED TO THE SHARED TRIP by the store. That
// is stated here rather than checked, because checking it would be the second
// implementation this file's header refuses — and the store's own leg, at
// fixture scale, is what holds it.
type PublicSource struct {
	Trip   Trip
	Cities []City
	Places []Place
	Photos []Photo
	Walks  []Walk
}

// Mint turns an object id into a signed URL a stranger can fetch. The public
// read supplies one that presigns at `media.Public`; a test supplies one that
// counts.
//
// IT IS A PARAMETER AND NOT A DEPENDENCY OF THIS PACKAGE. internal/logbook
// imports no bucket and reads no environment; what it owns is WHETHER a URL
// may be minted, which is a decision about the flags, and the mint itself
// belongs to whoever holds the credentials.
type Mint func(objectID string) (string, error)

// EmitPublic applies the allowlist and the three flags.
//
// THE ORDER OF THE TWO HALVES IS THE WHOLE OF `sharePhotos`. The rule is about
// the MINT and not about the field: a presigned URL is a live capability from
// the moment it is signed, whether or not it reaches a response body, and
// presigning is offline arithmetic — so "minted and dropped" leaves no trace
// anywhere and is indistinguishable from "never minted" in every log there is.
// This function therefore does not mint and discard; with `sharePhotos` false
// it never calls `mint` at all, and the leg that holds that COUNTS calls,
// because an assertion about the response body cannot see a URL that was
// minted and thrown away.
//
// EVERY LIST IS ALWAYS PRESENT AND ALWAYS A LIST, which is `Emit`'s rule and
// for its reason: a nil slice marshals to `null`, and `null` is neither an
// absent key nor an empty list — it is the one shape a `List<dynamic>` cast
// throws on. `sharePhotos: false` makes `photos` an EMPTY ARRAY, never a
// missing key, and that is honest as well as safe: the trip HAS photographs
// and the owner has chosen not to publish them, which is a different statement
// from "this shape has no photographs key".
//
// IT STAMPS FormatVersion RATHER THAN TAKING ONE, WHICH IS THE OPPOSITE OF
// Emit. `Emit` takes the version because DEC-53's negotiation is a thing an
// authenticated client can do — it sends `X-Logbook-Format` and the server
// answers 406 with what it can write. A stranger holding a URL has no
// negotiation surface at all, so a parameter here would be a branch nothing
// could ever exercise. The day this document has two shapes, this grows the
// parameter together with the caller that chooses.
func EmitPublic(src PublicSource, mint Mint) (Public, error) {
	out := Public{
		Version: FormatVersion,
		Trip: PublicTrip{
			ID:      src.Trip.ID,
			Name:    src.Trip.Name,
			CityIDs: orEmpty(src.Trip.CityIDs),
			Start:   src.Trip.Start,
			End:     src.Trip.End,
			Summary: src.Trip.Summary,
		},
		Cities: make([]PublicCity, 0, len(src.Cities)),
		Places: make([]PublicPlace, 0, len(src.Places)),
		Photos: make([]PublicPhoto, 0, len(src.Photos)),
		Walks:  make([]PublicWalk, 0, len(src.Walks)),
	}

	if src.Trip.SharePhotos && src.Trip.CoverAsset != nil {
		url, err := mint(*src.Trip.CoverAsset)
		if err != nil {
			return Public{}, err
		}
		out.Trip.CoverURL = &url
	}

	for _, city := range src.Cities {
		// THE CENTRE STAYS EVEN WITH shareCoordinates OFF, and this is the one
		// place in the document where a coordinate survives that flag. A city
		// centre is coarse — it IS a city — and it is what a map opens on when
		// there are no pins to fit. Removing it would leave a share page that
		// cannot draw a map at all, which is a different product rather than a
		// more private one.
		out.Cities = append(out.Cities, PublicCity{
			ID: city.ID, Name: city.Name, Country: city.Country, Centre: city.Centre,
		})
	}

	for _, place := range src.Places {
		published := PublicPlace{
			ID:     place.ID,
			CityID: place.CityID,
			Name:   place.Name,
			Days:   make([]PublicDay, 0, len(place.Visits)),
		}
		if src.Trip.ShareCoordinates {
			at := place.Coordinates
			published.Coordinates = &at
		}
		for _, visit := range place.Visits {
			day := PublicDay{At: visit.At, Note: visit.Note}
			if !src.Trip.ShareNotes {
				day.Note = nil
			}
			published.Days = append(published.Days, day)
		}
		out.Places = append(out.Places, published)
	}

	if src.Trip.SharePhotos {
		for _, photo := range src.Photos {
			url, err := mint(photo.Asset)
			if err != nil {
				return Public{}, err
			}
			published := PublicPhoto{
				ID:      photo.ID,
				CityID:  photo.CityID,
				PlaceID: photo.PlaceID,
				TakenAt: photo.TakenAt,
				URL:     url,
				Caption: photo.Caption,
			}
			if !src.Trip.ShareNotes {
				// BOTH HALVES OF ONE PROMISE. A note on a visit and a note on
				// a photograph are the same thing to the person who wrote
				// them; stripping one and not the other is the mutation the
				// notes leg exists for.
				published.Caption = nil
			}
			if src.Trip.ShareCoordinates {
				published.Coordinates = photo.Coordinates
				published.AccuracyMetres = photo.AccuracyMetres
			}
			out.Photos = append(out.Photos, published)
		}
	}

	for _, walk := range src.Walks {
		published := PublicWalk{
			ID:         walk.ID,
			CityID:     walk.CityID,
			RecordedOn: walk.RecordedOn,
			DistanceKm: walk.DistanceKm,
			Points:     []LatLng{},
		}
		// A TRACK WHOSE POINTS ARE WITHHELD IS STILL A DAY THAT HAPPENED, so
		// the walk is published with an empty `points` rather than dropped:
		// `distanceKm` and the day it was recorded are not coordinates.
		if src.Trip.ShareCoordinates {
			published.Points = orEmpty(walk.Points)
		}
		out.Walks = append(out.Walks, published)
	}

	return out, nil
}
