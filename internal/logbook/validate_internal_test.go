package logbook

import "testing"

// THE EXPRESSION THE PLAN NAMES, IN ONE FALSIFIABLE PLACE.
//
// `allowedContentTypes` is the one runtime list and `contentTypePattern` is
// built from it, so nothing in validate.go spells `^image/(jpeg|png)$`. This
// is where that string lives, and it is a leg rather than a second literal:
// add a type, drop a type, or reorder the list, and it reddens with the
// expression the code actually compiled.
func TestTheContentTypeExpressionIsTheOneDEC104Narrowed(t *testing.T) {
	if got, want := contentTypePattern.String(), `^image/(jpeg|png)$`; got != want {
		t.Errorf("contentTypePattern = %q, want %q — DEC-104 took `heic` out and "+
			"`image/jpeg` stays because internal/postgres/schema_test.go seeds "+
			"shared fixtures with it", got, want)
	}
}
