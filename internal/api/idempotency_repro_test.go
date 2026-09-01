package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const reproSecret = "test-app-secret"

func sign(t *testing.T, body string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(reproSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookRetryDuplicatesTheReply drives the real HTTP handler with the exact
// same signed payload twice, which is what Meta does when it does not get a fast
// 200. Storage is idempotent via the whatsappMediaId unique index, but the reply
// is not, so the human gets the message twice.
func TestWebhookRetryDuplicatesTheReply(t *testing.T) {
	rep := &fakeReplier{}
	srv := NewServer(
		config.Config{WhatsAppAppSecret: reproSecret},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps{Replier: rep, Relay: relay.New([]string{"+" + alice, "+" + bob})},
	)
	handler := srv.Routes()

	body := `{"object":"whatsapp_business_account","entry":[{"id":"E","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","messages":[{"from":"` + alice + `","id":"wamid.SAME","timestamp":"1","type":"text","text":{"body":"help"}}]}}]}]}`

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/whatsapp/webhook", strings.NewReader(body))
		req.Header.Set(whatsapp.SignatureHeader, sign(t, body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("delivery %d: status %d, want 200", i+1, rec.Code)
		}
	}

	if got := len(rep.sent); got != 1 {
		t.Fatalf("sent %d messages for one logical message delivered twice, want 1: %v",
			got, rep.recipients())
	}
}
