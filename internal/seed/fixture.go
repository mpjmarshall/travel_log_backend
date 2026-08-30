// The captured client document -> a Dataset, which is one row per column of
// the ten tables the load writes.
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
func FromDocument(tr Traveller, objects []MediaObject, doc logbook.Document) (*Dataset, error) {
	if doc.Traveller != nil && doc.Traveller.Name != "" && tr.Name == nil {
		name := doc.Traveller.Name
		tr.Name = &name
	}
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
		for i, cityID := range t.CityIDs {
			d.TripCities = append(d.TripCities, TripCity{
				TravellerID: tr.ID, TripID: t.ID, CityID: cityID, Ordinal: i,
			})
		}
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

// dayLiteral is what a `date` column is bound with.
func dayLiteral(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(dateLayout)
	return &s
}
