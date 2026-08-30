// The public envelope: what `GET /l/{token}` answers, key for key.
package logbook

// Public is the whole answer: exactly six keys.
type Public struct {
	Version int           `json:"version"`
	Trip    PublicTrip    `json:"trip"`
	Cities  []PublicCity  `json:"cities"`
	Places  []PublicPlace `json:"places"`
	Photos  []PublicPhoto `json:"photos"`
	Walks   []PublicWalk  `json:"walks"`
}

// PublicTrip is seven keys.
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
type PublicCity struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Country Country `json:"country"`
	Centre  LatLng  `json:"centre"`
}

// PublicPlace is five keys, and `days` is the one rename in this document.
type PublicPlace struct {
	ID          string      `json:"id"`
	CityID      string      `json:"cityId"`
	Name        string      `json:"name"`
	Coordinates *LatLng     `json:"coordinates"`
	Days        []PublicDay `json:"days"`
}

// PublicDay is what a Visit becomes: two keys.
type PublicDay struct {
	At   Instant `json:"at"`
	Note *string `json:"note"`
}

// PublicPhoto is eight keys.
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
type PublicWalk struct {
	ID         string   `json:"id"`
	CityID     string   `json:"cityId"`
	RecordedOn Instant  `json:"recordedOn"`
	DistanceKm float64  `json:"distanceKm"`
	Points     []LatLng `json:"points"`
}

// PublicSource is what the store hands over: the rows that are already
// allowed to be here, in the domain's own types.
type PublicSource struct {
	Trip   Trip
	Cities []City
	Places []Place
	Photos []Photo
	Walks  []Walk
}

// Mint turns an object id into a signed URL a stranger can fetch.
type Mint func(objectID string) (string, error)

// EmitPublic applies the allowlist and's three flags.
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
		if src.Trip.ShareCoordinates {
			published.Points = orEmpty(walk.Points)
		}
		out.Walks = append(out.Walks, published)
	}

	return out, nil
}
