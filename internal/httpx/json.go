// The only file in this repository that imports encoding/json, and's two
// functions in it are the only two that use it.
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
// body.
const MaxBodyBytes int64 = 1 << 20

// The two transport sentinels.
var (
	ErrInvalidBody  = errors.New("httpx: the request body is not the JSON this route expects")
	ErrBodyTooLarge = errors.New("httpx: the request body is larger than the limit")
)

// WriteJSON writes v at status, as JSON.
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
