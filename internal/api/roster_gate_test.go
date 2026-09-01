package api

import (
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

func newGatedServer(t *testing.T, numbers []string) (http.Handler, *fakeReplier) {
	t.Helper()

	rep := &fakeReplier{}
	srv := NewServer(
		config.Config{WhatsAppAppSecret: reproSecret},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps{Replier: rep, Relay: relay.New(numbers)},
	)
	return srv.Routes(), rep
}

func postHelp(t *testing.T, handler http.Handler, from string) {
	t.Helper()

	body := `{"object":"whatsapp_business_account","entry":[{"id":"E","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","messages":[{"from":"` + from + `","id":"wamid.GATE","timestamp":"1","type":"text","text":{"body":"help"}}]}}]}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/whatsapp/webhook", strings.NewReader(body))
	req.Header.Set(whatsapp.SignatureHeader, sign(t, body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: Meta must still get its 200 or it retries forever", rec.Code)
	}
}

func TestConfiguredRosterIgnoresAStranger(t *testing.T) {
	handler, rep := newGatedServer(t, []string{"+" + alice, "+" + bob})

	postHelp(t, handler, stranger)

	if got := len(rep.sent); got != 0 {
		t.Fatalf("sent %d messages to %s, who is not on the roster, want 0: %v",
			got, stranger, rep.recipients())
	}
}

func TestEmptyRosterAnswersAnyone(t *testing.T) {
	handler, rep := newGatedServer(t, nil)

	postHelp(t, handler, stranger)

	if got := len(rep.sent); got != 1 {
		t.Fatalf("sent %d messages, want 1: an empty WHATSAPP_RELAY_NUMBERS means the relay was "+
			"never configured and the bot is in single-user mode, so ignoring the sender would "+
			"brick the deployment: %v", got, rep.recipients())
	}
}
