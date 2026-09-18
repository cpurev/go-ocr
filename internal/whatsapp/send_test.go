package whatsapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendTextReturnsTheMessageID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","contacts":[{"input":"46","wa_id":"46"}],"messages":[{"id":"wamid.ABC"}]}`))
	}))
	defer srv.Close()

	id, err := NewSender(srv.URL, "token", "123", time.Second).SendText(context.Background(), "46", "hi")
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if id != "wamid.ABC" {
		t.Fatalf("SendText id = %q, want wamid.ABC: a failed status can only be traced by it", id)
	}
}

func TestSendTextMapsReEngagementToOutsideWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Re-engagement message","code":131047}}`))
	}))
	defer srv.Close()

	_, err := NewSender(srv.URL, "token", "123", time.Second).SendText(context.Background(), "46", "hi")
	if !errors.Is(err, ErrOutsideWindow) {
		t.Fatalf("SendText error = %v, want ErrOutsideWindow", err)
	}
}
