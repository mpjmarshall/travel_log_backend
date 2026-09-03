// The route table, declared as data and registered from the slice.
package httpapi

import "net/http"

// Limit names which limiter a route counts against — not a number.
type Limit int

const (
	LimitCredential Limit = iota

	LimitTraveller

	LimitPublic
)

func (l Limit) String() string {
	switch l {
	case LimitTraveller:
		return "traveller"
	case LimitPublic:
		return "public"
	default:
		return "credential"
	}
}

// Route is one row of the table.
type Route struct {
	Method  string
	Pattern string
	Handler http.HandlerFunc
	Auth    bool

	Limit Limit

	NoStore bool
}

// Routes is the whole API surface.
func Routes(deps Deps) []Route {
	return []Route{
		{http.MethodPost, "/v1/auth/register", register(deps.Log, deps.Auth), false, LimitCredential, false},
		{http.MethodPost, "/v1/auth/code", requestCode(deps.Log, deps.Auth, deps.Mail), false, LimitCredential, false},
		{http.MethodPost, "/v1/auth/session", signIn(deps.Log, deps.Auth), false, LimitCredential, false},
		{http.MethodGet, "/v1/logbook", readLogbook(deps.Log, deps.Logbook), true, LimitTraveller, false},
		{http.MethodPut, "/v1/trips/{id}", putTrip(deps.Log, deps.Logbook), true, LimitTraveller, false},

		{http.MethodDelete, "/v1/trips/{id}", deleteTrip(deps.Log, deps.Logbook), true, LimitTraveller, false},

		{http.MethodGet, "/v1/cities/search", searchCities(deps.Log, deps.Geocode), true, LimitTraveller, true},
		{http.MethodPut, "/v1/cities/{id}", putCity(deps.Log, deps.Cities), true, LimitTraveller, false},
		{http.MethodPut, "/v1/places/{id}", putPlace(deps.Log, deps.Places), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/places/{id}", removePlace(deps.Log, deps.Places), true, LimitTraveller, false},

		{http.MethodPost, "/v1/photos/snooze", snoozePhotos(deps.Log, deps.Photos), true, LimitTraveller, false},
		{http.MethodPut, "/v1/photos/{id}", putPhoto(deps.Log, deps.Photos), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/photos/{id}", deletePhoto(deps.Log, deps.Photos), true, LimitTraveller, false},
		{http.MethodPost, "/v1/photos/{id}/refile", refilePhoto(deps.Log, deps.Photos), true, LimitTraveller, false},
		{http.MethodPut, "/v1/walks/{id}", putWalk(deps.Log, deps.Walks), true, LimitTraveller, false},

		{http.MethodPut, "/v1/trips/{id}/share", setShareOptions(deps.Log, deps.Share), true, LimitTraveller, false},
		{http.MethodPost, "/v1/trips/{id}/share", newShareLink(deps.Log, deps.Share), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/trips/{id}/share", stopSharing(deps.Log, deps.Share), true, LimitTraveller, false},

		{http.MethodPatch, "/v1/me", patchMe(deps.Log, deps.Logbook), true, LimitTraveller, false},

		{http.MethodDelete, "/v1/auth/session", revokeSession(deps.Log, deps.Auth), true, LimitTraveller, false},

		{http.MethodPost, "/v1/media", beginMedia(deps.Log, deps.Media, deps.Objects, deps.MediaMaxBytes, deps.Clock()), true, LimitTraveller, true},
		{http.MethodPost, "/v1/media/{id}/commit", commitMedia(deps.Log, deps.Media, deps.Objects), true, LimitTraveller, false},
		{http.MethodPost, "/v1/media/mint", mintMedia(deps.Log, deps.Media, deps.Objects), true, LimitTraveller, true},

		{http.MethodGet, "/l/{token}", publicShare(deps.Log, deps.Public, deps.Objects), false, LimitPublic, true},
	}
}
