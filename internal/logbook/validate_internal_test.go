package logbook

import "testing"

// the expression the plan names, in one falsifiable place.
func TestTheContentTypeExpressionIsTheOneDEC104Narrowed(t *testing.T) {
	if got, want := contentTypePattern.String(), `^image/(jpeg|png)$`; got != want {
		t.Errorf("contentTypePattern = %q, want %q — DEC-104 took `heic` out and "+
			"`image/jpeg` stays because internal/postgres/schema_test.go seeds "+
			"shared fixtures with it", got, want)
	}
}
