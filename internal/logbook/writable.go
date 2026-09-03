// The rules a write must satisfy that need a fact from storage to decide,
// called under the traveller's lock. Shape rules live in validate.go.
package logbook

// CheckWriteID refuses a write body carrying no id. It is unreachable over
// HTTP: a handler fills the id from the path before any store sees the body.
func CheckWriteID(id *string) error {
	if id == nil {
		return InvalidFieldError{Field: "id",
			Why: "a write needs the id it is writing to, and this body carries none"}
	}
	return nil
}
