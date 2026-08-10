package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type Envelope struct {
	Success bool       `json:"success"`
	Data    any        `json:"data,omitempty"`
	Error   *ErrorBody `json:"error,omitempty"`
	Meta    *Meta      `json:"meta,omitempty"`
}

type ErrorBody struct {
	Message string `json:"message"`

	Fields map[string]string `json:"fields,omitempty"`

	RequestID string `json:"request_id,omitempty"`
}

type Meta struct {
	Total  int `json:"total"`
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

func WriteJSON(w http.ResponseWriter, status int, payload Envelope) {
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("httpx: encoding response failed", "error", err)
		http.Error(w, `{"success":false,"error":{"message":"internal server error"}}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if _, err := w.Write(body); err != nil {
		slog.Debug("httpx: writing response failed", "error", err)
	}
}

func OK(w http.ResponseWriter, status int, data any, meta *Meta) {
	WriteJSON(w, status, Envelope{Success: true, Data: data, Meta: meta})
}

func Error(w http.ResponseWriter, r *http.Request, status int, message string, fields map[string]string) {
	WriteJSON(w, status, Envelope{
		Success: false,
		Error: &ErrorBody{
			Message:   message,
			Fields:    fields,
			RequestID: RequestIDFrom(r.Context()),
		},
	})
}
