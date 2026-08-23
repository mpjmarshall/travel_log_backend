// Reading the credential off the request, and carrying the traveller down the
// chain.
//
// THIS FILE WRITES NO RESPONSE AND IMPORTS NO httpx, and that is a boundary
// rather than an omission. DEC-62 asks for ONE mapping from a domain sentinel
// to a wire code and DEC-74 puts the HTTP layer in internal/httpapi, so a
// middleware here that answered 401 would be a second place in the tree that
// knows the wire vocabulary. What lives here is the two things the domain
// genuinely owns: what a bearer credential looks like, and who the request is
// for. internal/httpapi/middleware.go is the middleware, and it is the only
// caller of both.
package auth

import (
	"context"
	"net/http"
	"strings"
)

// travellerKey is an unexported empty struct, so nothing outside this package
// can write to the slot. A string key is writable by anything that can spell
// it — including a middleware from a package that has never heard of this one.
type travellerKey struct{}

// Bearer reads `Authorization: Bearer <token>`.
//
// THE SCHEME IS COMPARED CASE-INSENSITIVELY (RFC 7235) AND THE TOKEN IS NOT
// TOUCHED AT ALL. A session token is 43 characters of base64url and holds no
// space, so anything with one in it is not a token this build minted — and
// trimming or rejoining would make two different strings into one credential
// and ask the store about something the client never sent.
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

// TravellerFrom reads it back, and answers false rather than a zero value: a
// handler that read a zero Traveller and believed it would serve one
// traveller's log to everybody.
func TravellerFrom(ctx context.Context) (Traveller, bool) {
	tr, ok := ctx.Value(travellerKey{}).(Traveller)
	return tr, ok
}
