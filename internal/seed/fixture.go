// The captured client document -> a Dataset, which is one row per column of
// the ten tables the load writes.
//
// TWO MAPPINGS NEED CARE AND BOTH ARE LOAD-BEARING ON READ:
//
//   - VISIT ORDINALS are assigned in the fixture's OWN ARRAY ORDER, because
//     the client reads `visits.first.at` as "last visited" and the captured
//     document is newest-first. `readVisitsSQL` orders by `place_id, ordinal,
//     id`, so the ordinal is the only thing carrying that order across
//     storage. Sorting by date here — the obvious "tidy" thing — silently
//     rebinds every P1 header to the wrong day, with every individual field
//     still correct.
//   - TRIP_CITIES ORDINALS are assigned in `cityIds` order, because that list
//     is TRAVEL ORDER (DEC-64, DEC-26) rather than a set.
//
// THE DATES ARE FORMATTED AS `date` LITERALS AND NOT AS TIMESTAMPS.
// `trips.started_on`, `trips.ended_on` and `walks.recorded_on` are `date`
// columns (DEC-68); binding a time.Time and letting PostgreSQL cast makes the
// answer depend on the SESSION's TimeZone, which is a fact about the container
// rather than about the log. `2027-09-17` is the same day in every timezone.
package seed

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"travellog/internal/logbook"
)

// dateLayout is the `date` column's own literal form.
const dateLayout = "2006-01-02"

// FromDocument turns a rewritten logbook document into one whole database's
// worth of rows.
//
// IT TAKES THE TRAVELLER ROW RATHER THAN AN ID, and that is a divergence from
// the plan's `FromDocument(travellerID, doc)` with a reason. DEC-97 generates
// the passphrase PER RUN so a fixture cannot become a shipped credential, so
// the credential is made by the command and cannot be invented here — and a
// travellers row with an empty passphrase_hash is refused by
// `travellers_passphrase_hash_present_ck` five statements before anything
// interesting happens.
//
// IT TAKES THE MEDIA OBJECTS TOO, AND REFUSES A DOCUMENT THAT ADDRESSES ONE
// THEY DO NOT DECLARE. `photos_asset_fk` would refuse it as well — five tables
// later, as SQLSTATE 23503 naming a constraint. Here the refusal names the
// digest nothing uploaded.
func FromDocument(tr Traveller, objects []MediaObject, doc logbook.Document) (*Dataset, error) {
	if doc.Traveller != nil && doc.Traveller.Name != "" && tr.Name == nil {
		name := doc.Traveller.Name
		tr.Name = &name
	}
	// A LOADED LOG IS AT VERSION 1 AND NOT 0. A traveller at 0 is served 200
	// with NO ETag by design, which is right for a log nobody has written and
	// wrong for one that was just loaded: every phone would re-fetch it for
	// ever.
	if tr.LogbookVersion < 1 {
		tr.LogbookVersion = 1
	}

	declared := map[string]bool{}
	for _, o := range objects {
		declared[o.ID] = true
	}
	require := func(id, where string) error {
		if !declared[id] {
			return fmt.Errorf("seed: %s addresses object %s, which nothing in this "+
				"load has uploaded; a media_objects row it can reference has to exist "+
				"first (DEC-78)", where, id)
		}
		return nil
	}

	d := &Dataset{Travellers: []Traveller{tr}, MediaObjects: objects}
	at := tr.CreatedAt

	for _, c := range doc.Cities {
		if c.CoverAsset != nil {
			if err := require(*c.CoverAsset, "city "+c.ID+"'s cover"); err != nil {
				return nil, err
			}
		}
		d.Cities = append(d.Cities, City{
			TravellerID: tr.ID, ID: c.ID, Name: c.Name,
			CountryCode: c.Country.Code, CountryName: c.Country.Name,
			CentreLat: c.Centre.Lat, CentreLng: c.Centre.Lng,
			CoverAsset: c.CoverAsset, CreatedAt: at,
		})
	}

	for _, t := range doc.Trips {
		if t.CoverAsset != nil {
			if err := require(*t.CoverAsset, "trip "+t.ID+"'s cover"); err != nil {
				return nil, err
			}
		}
		d.Trips = append(d.Trips, Trip{
			TravellerID: tr.ID, ID: t.ID, Name: t.Name,
			StartedOn: dayOrNil(t.Start), EndedOn: dayOrNil(t.End),
			Summary: t.Summary, CoverAsset: t.CoverAsset,
			SharePhotos: t.SharePhotos, ShareNotes: t.ShareNotes,
			ShareCoordinates: t.ShareCoordinates, CreatedAt: at,
		})
		// TRAVEL ORDER, and the ordinal is the only thing that carries it.
		for i, cityID := range t.CityIDs {
			d.TripCities = append(d.TripCities, TripCity{
				TravellerID: tr.ID, TripID: t.ID, CityID: cityID, Ordinal: i,
			})
		}
		// THE SHARE LINK IS THE CAPTURED TOKEN'S DIGEST, AND THIS LINE IS
		// WHERE THE PLAINTEXT STOPS. R4 wrote the plaintext straight into the
		// column and said in this comment that it was "the LAST release in
		// which that sentence is true"; 0004 is that release (DEC-85). The
		// captured `shareLinkId` is still what the fixture holds — it has to
		// be, because `/l/{token}` has to resolve for the token the client
		// document names — and `HashShareToken` is the only thing that leaves
		// this scope with it.
		//
		// WHAT SURVIVES THE CHANGE IS `shared`, derived from this row's
		// existence and its `revoked_at`, which is why the round-trip leg
		// substitutes `Shared = ShareLinkID != nil` and then nils the token.
		if t.ShareLinkID != nil {
			d.ShareLinks = append(d.ShareLinks, ShareLink{
				TravellerID: tr.ID, TripID: t.ID,
				TokenHash: logbook.HashShareToken(*t.ShareLinkID), CreatedAt: at,
			})
		}
	}

	for _, p := range doc.Places {
		if p.CoverAsset != nil {
			if err := require(*p.CoverAsset, "place "+p.ID+"'s cover"); err != nil {
				return nil, err
			}
		}
		d.Places = append(d.Places, Place{
			TravellerID: tr.ID, ID: p.ID, CityID: p.CityID, Name: p.Name,
			Lat: p.Coordinates.Lat, Lng: p.Coordinates.Lng,
			Plan: p.Plan, CoverAsset: p.CoverAsset, CreatedAt: at,
		})
		// THE FIXTURE'S OWN ARRAY ORDER. See the file comment: the client reads
		// visits.first.at as lastVisited, so this ordinal is what "newest
		// first" is made of once the list has been through storage.
		for i, v := range p.Visits {
			d.Visits = append(d.Visits, Visit{
				TravellerID: tr.ID, ID: v.ID, PlaceID: v.PlaceID, TripID: v.TripID,
				Ordinal: i, At: v.At.Time(), Note: v.Note, CreatedAt: at,
			})
		}
	}

	for _, p := range doc.Photos {
		if err := require(p.Asset, "photograph "+p.ID); err != nil {
			return nil, err
		}
		photo := Photo{
			TravellerID: tr.ID, ID: p.ID, TripID: p.TripID, CityID: p.CityID,
			PlaceID: p.PlaceID, VisitID: p.VisitID, TakenAt: p.TakenAt.Time(),
			Asset: p.Asset, Caption: p.Caption, AccuracyMetres: p.AccuracyMetres,
			CreatedAt: at,
		}
		if p.Coordinates != nil {
			lat, lng := p.Coordinates.Lat, p.Coordinates.Lng
			photo.Lat, photo.Lng = &lat, &lng
		}
		if p.FiledLater != nil {
			filed := p.FiledLater.Time()
			photo.FiledLater = &filed
		}
		d.Photos = append(d.Photos, photo)
	}

	for _, w := range doc.Walks {
		d.Walks = append(d.Walks, Walk{
			TravellerID: tr.ID, ID: w.ID, TripID: w.TripID, CityID: w.CityID,
			RecordedOn: w.RecordedOn.Time(), DistanceKm: w.DistanceKm,
			Points: pointsJSON(w.Points), Name: w.Name, Dismissed: w.Dismissed,
			CreatedAt: at,
		})
	}

	return d, nil
}

// pointsJSON renders a track as the jsonb array the column holds.
//
// IT IS WRITTEN BY HAND AND NOT BY encoding/json, and that is a constraint
// rather than a preference: internal/httpx's AST sweep names every file in
// this module that may import the encoder, and this is not one of them (spec
// L19 is about payload encoding). The shape is two float keys, which is small
// enough to write once and assert against the round trip.
//
// 'g' with -1 precision is the SHORTEST form that round-trips exactly, which is
// what `(pt.value->>'lat')::double precision` reads back, and what the emitter
// prints on the way out.
func pointsJSON(points []logbook.LatLng) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, p := range points {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"lat":`)
		b.WriteString(strconv.FormatFloat(p.Lat, 'g', -1, 64))
		b.WriteString(`,"lng":`)
		b.WriteString(strconv.FormatFloat(p.Lng, 'g', -1, 64))
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String()
}

func dayOrNil(i *logbook.Instant) *time.Time {
	if i == nil {
		return nil
	}
	t := i.Time()
	return &t
}

// dayLiteral is what a `date` column is bound with. See the file comment.
func dayLiteral(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(dateLayout)
	return &s
}
