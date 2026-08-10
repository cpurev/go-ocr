package httpx

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type QueryParams map[string][]string

func Params(r *http.Request) QueryParams {
	return QueryParams(r.URL.Query())
}

func (q QueryParams) String(key string) string {
	values, ok := q[key]
	if !ok || len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func (q QueryParams) Int(key string, fallback int) (int, error) {
	raw := q.String(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number, got %q", key, raw)
	}
	return v, nil
}

func (q QueryParams) Float(key string) (*float64, error) {
	raw := q.String(key)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a number, got %q", key, raw)
	}
	return &v, nil
}
