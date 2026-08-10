package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxBodyBytes = 1 << 20

func DecodeJSON[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var payload T

	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return payload, fmt.Errorf("Content-Type must be application/json, got %q", ct)
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)

	dec.DisallowUnknownFields()

	if err := dec.Decode(&payload); err != nil {
		return payload, decodeError(err)
	}

	if dec.More() {
		return payload, errors.New("body must contain exactly one JSON object")
	}

	return payload, nil
}

func decodeError(err error) error {
	var (
		syntaxErr    *json.SyntaxError
		typeErr      *json.UnmarshalTypeError
		maxBytesErr  *http.MaxBytesError
		invalidUnmar *json.InvalidUnmarshalError
	)

	switch {
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("body contains malformed JSON at character %d", syntaxErr.Offset)

	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return fmt.Errorf("field %q has the wrong type (expected %s)", typeErr.Field, typeErr.Type)
		}
		return fmt.Errorf("body contains a value of the wrong type (expected %s)", typeErr.Type)

	case errors.As(err, &maxBytesErr):
		return fmt.Errorf("body must be no larger than %d bytes", maxBytesErr.Limit)

	case errors.Is(err, io.EOF):
		return errors.New("body must not be empty")

	case errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("body contains truncated JSON")

	case strings.HasPrefix(err.Error(), "json: unknown field "):

		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return fmt.Errorf("body contains unknown field %s", field)

	case errors.As(err, &invalidUnmar):

		panic(err)

	default:
		return err
	}
}
