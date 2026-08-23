// The only file in this repository that imports encoding/json, and the two
// functions in it are the only two that use it.
//
// go_backend.md L19: "Exclusively use `encoding/json` for payload encoding and
// decoding." sweep_test.go enforces BOTH halves by walking the AST — the file
// monopoly and the two-function monopoly — because the step's own acceptance
// check is a grep, and a grep matches its own source, matches comments, and
// cannot tell an import from a mention.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// MaxBodyBytes is the ceiling http.MaxBytesReader enforces on every decoded
// body. 1 MiB. The largest body any route in the slice takes is one trip.
const MaxBodyBytes int64 = 1 << 20

// The two transport sentinels. They are the transport's half of DEC-62's
// idiom: CodeFor turns them into wire words, and a handler never switches on
// them itself.
var (
	ErrInvalidBody  = errors.New("httpx: the request body is not the JSON this route expects")
	ErrBodyTooLarge = errors.New("httpx: the request body is larger than the limit")
)

// WriteJSON writes v at status, as JSON.
//
// IT MARSHALS TO A BUFFER FIRST, AND THAT IS THE DECISION IN THIS FUNCTION.
// json.NewEncoder(w).Encode writes as it walks: a type that fails to marshal
// half way through has already had its first bytes and an implicit 200 sent,
// so the client receives a TRUNCATED body under a success status and cannot
// tell it from a short read. Marshalling first means the failure happens while
// nothing has been written and the response can still be an honest 500.
//
// It also does not use json.Encoder for a smaller reason worth recording:
// Encoder appends a newline, so the emitted bytes would not equal
// json.Marshal's, and the prebuilt bodies in errors.go are guarded by exactly
// that byte equality.
func WriteJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.ErrorContext(r.Context(), "httpx: the response could not be encoded",
			slog.String("requestId", RequestIDFrom(r.Context())),
			slog.String("err", err.Error()),
		)
		w.Header().Set("Content-Type", contentTypeJSON)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, bodyInternal)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// DecodeJSON reads one JSON value from the request body into dst.
//
// THREE THINGS IT DOES, AND THE THIRD IS AN OMISSION ON PURPOSE:
//
//   - http.MaxBytesReader, at MaxBodyBytes. It takes w as well as the body
//     because that is what lets it close the connection rather than read a
//     gigabyte to discover it should not have.
//   - It refuses TRAILING CONTENT. `{"id":"kyoto"}{"id":"osaka"}` decodes the
//     first value and, without the second Decode below, silently discards the
//     rest — a client sending two documents would be told its second write
//     succeeded. io.EOF from that second Decode is what means "the body held
//     exactly one value"; anything else is a second value, trailing junk, or a
//     body that only now crosses the size limit.
//   - DisallowUnknownFields is DELIBERATELY OFF (DEC-13). Every server addition
//     is additive-and-optional, which is a promise about BOTH directions: a
//     client built against a later API sends a key this build has never heard
//     of, and rejecting it would make every additive change a breaking one.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: the body carries more than one JSON value", ErrInvalidBody)
		}
		return decodeError(err)
	}
	return nil
}

func decodeError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("%w: the limit is %d bytes", ErrBodyTooLarge, tooLarge.Limit)
	}
	return fmt.Errorf("%w: %v", ErrInvalidBody, err)
}
