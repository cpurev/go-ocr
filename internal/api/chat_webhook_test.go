package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cpurev/go-ocr/internal/whatsapp"
)

// Reaching the other phone is the bot's whole job, so a verb word at the front
// of ordinary English must not turn a message into a private answer.
func TestChatStartingWithAVerbWordReachesTheOtherPhone(t *testing.T) {
	handler, rep := newGatedServer(t, []string{"+" + alice, "+" + bob})

	const chat = "who is coming tonight"
	body := `{"object":"whatsapp_business_account","entry":[{"id":"E","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","messages":[{"from":"` + alice + `","id":"wamid.CHAT","timestamp":"1","type":"text","text":{"body":"` + chat + `"}}]}}]}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/whatsapp/webhook", strings.NewReader(body))
	req.Header.Set(whatsapp.SignatureHeader, sign(t, body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}

	if got := len(rep.sent); got != 1 {
		t.Fatalf("%q produced %d messages, want 1: %v", chat, got, rep.recipients())
	}
	if rep.sent[0].to != bob {
		t.Fatalf("%q was answered to %s, want it forwarded to %s", chat, rep.sent[0].to, bob)
	}
	if want := "From +" + alice + ":\n\n" + chat; rep.sent[0].body != want {
		t.Errorf("forwarded body %q, want %q", rep.sent[0].body, want)
	}
}
