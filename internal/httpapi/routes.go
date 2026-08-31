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
		{http.MethodPost, "/v1/auth/register", register(deps), false, LimitCredential, false},
		{http.MethodPost, "/v1/auth/code", requestCode(deps), false, LimitCredential, false},
		{http.MethodPost, "/v1/auth/session", signIn(deps), false, LimitCredential, false},
		{http.MethodGet, "/v1/logbook", readLogbook(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/trips/{id}", putTrip(deps), true, LimitTraveller, false},

		{http.MethodDelete, "/v1/trips/{id}", deleteTrip(deps), true, LimitTraveller, false},

		{http.MethodGet, "/v1/cities/search", searchCities(deps), true, LimitTraveller, true},
		{http.MethodPut, "/v1/cities/{id}", putCity(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/places/{id}", putPlace(deps), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/places/{id}", removePlace(deps), true, LimitTraveller, false},

		{http.MethodPost, "/v1/photos/snooze", snoozePhotos(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/photos/{id}", putPhoto(deps), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/photos/{id}", deletePhoto(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/photos/{id}/refile", refilePhoto(deps), true, LimitTraveller, false},
		{http.MethodPut, "/v1/walks/{id}", putWalk(deps), true, LimitTraveller, false},

		{http.MethodPut, "/v1/trips/{id}/share", setShareOptions(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/trips/{id}/share", newShareLink(deps), true, LimitTraveller, false},
		{http.MethodDelete, "/v1/trips/{id}/share", stopSharing(deps), true, LimitTraveller, false},

		{http.MethodPatch, "/v1/me", patchMe(deps), true, LimitTraveller, false},

		{http.MethodDelete, "/v1/auth/session", revokeSession(deps), true, LimitTraveller, false},

		{http.MethodPost, "/v1/media", beginMedia(deps), true, LimitTraveller, true},
		{http.MethodPost, "/v1/media/{id}/commit", commitMedia(deps), true, LimitTraveller, false},
		{http.MethodPost, "/v1/media/mint", mintMedia(deps), true, LimitTraveller, true},

		{http.MethodGet, "/l/{token}", publicShare(deps), false, LimitPublic, true},
	}
}
