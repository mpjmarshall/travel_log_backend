// The one emitter, and's two version numbers that are not the same number.
package logbook

import "errors"

// EmitterVersion is's first half.
const EmitterVersion int64 = 2

// FormatVersion is the `"version": 2`.
const FormatVersion = 2

// ErrUnsupportedFormat is a request for a version this build cannot write.
var ErrUnsupportedFormat = errors.New("logbook: no emitter for that format version")

// Envelope is the client's own two keys, and `decodeLogbook` reads exactly
// this.
type Envelope struct {
	Version int      `json:"version"`
	Logbook Document `json:"logbook"`
}

// Emit renders one document at the named format version.
func Emit(formatVersion int, doc Document) (Envelope, error) {
	if formatVersion != FormatVersion {
		return Envelope{}, ErrUnsupportedFormat
	}

	doc.Trips = orEmpty(doc.Trips)
	doc.Cities = orEmpty(doc.Cities)
	doc.Places = orEmpty(doc.Places)
	doc.Photos = orEmpty(doc.Photos)
	doc.Walks = orEmpty(doc.Walks)
	for i := range doc.Trips {
		doc.Trips[i] = EmitTrip(doc.Trips[i])
	}
	for i := range doc.Places {
		doc.Places[i].Visits = orEmpty(doc.Places[i].Visits)
	}
	for i := range doc.Walks {
		doc.Walks[i].Points = orEmpty(doc.Walks[i].Points)
	}

	return Envelope{Version: formatVersion, Logbook: doc}, nil
}

// EmitTrip is the same normalisation Emit applies to a trip inside the
// document, for the one route that answers a bare entity.
func EmitTrip(t Trip) Trip {
	t.CityIDs = orEmpty(t.CityIDs)
	return t
}

// EmitPlace is the same normalisation for `PUT /v1/places/{id}`, and it is
// the second time this rule has had to be written down.
func EmitPlace(p Place) Place {
	p.Visits = orEmpty(p.Visits)
	return p
}

// EmitWalk is the same normalisation for `PUT /v1/walks/{id}`, and it is the
// third time this rule has had to be written down.
func EmitWalk(w Walk) Walk {
	w.Points = orEmpty(w.Points)
	return w
}

// Formats is what a 406 names: every version this build can write.
func Formats() []int { return []int{FormatVersion} }

func orEmpty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}
