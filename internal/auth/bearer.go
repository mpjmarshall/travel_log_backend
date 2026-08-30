// Reading the credential off the request, and carrying the traveller down the
// chain.
package auth

import (
	"context"
	"net/http"
	"strings"
)

// travellerKey is an unexported empty struct, so nothing outside this package
// can write to the slot.
type travellerKey struct{}

// Bearer reads `Authorization: Bearer <token>`.
func Bearer(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

// WithTraveller puts the authenticated traveller on the context.
func WithTraveller(ctx context.Context, tr Traveller) context.Context {
	return context.WithValue(ctx, travellerKey{}, tr)
}

// TravellerFrom reads it back, and answers false rather than a zero value.
func TravellerFrom(ctx context.Context) (Traveller, bool) {
	tr, ok := ctx.Value(travellerKey{}).(Traveller)
	return tr, ok
}
