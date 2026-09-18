package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/httpx"
	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

// The service is public so Meta can reach the webhook, which made the REST
// routes public too: anyone with the URL could list every receipt.
func TestPrivateRoutesNeedTheAPIToken(t *testing.T) {
	receipts := &fakeReceipts{
		listReceipts: func(context.Context, store.ReceiptFilter) ([]model.Receipt, int64, error) {
			return []model.Receipt{{ID: "r1", UserID: alice}}, 1, nil
		},
	}

	for _, tt := range []struct {
		name, configured, sent string
		want                   int
	}{
		{"no token configured closes the route", "", "", http.StatusUnauthorized},
		{"no token configured ignores a guess", "", "Bearer ", http.StatusUnauthorized},
		{"missing header", "s3cret", "", http.StatusUnauthorized},
		{"wrong token", "s3cret", "Bearer nope", http.StatusUnauthorized},
		{"wrong scheme", "s3cret", "Basic s3cret", http.StatusUnauthorized},
		{"right token", "s3cret", "Bearer s3cret", http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(config.Config{APIToken: tt.configured}, discardLogger(),
				Deps{Receipts: receipts})

			for _, path := range []string{"/api/v1/receipts", "/api/v1/receipts/r1"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if tt.sent != "" {
					req.Header.Set("Authorization", tt.sent)
				}
				rec := httptest.NewRecorder()
				srv.Routes().ServeHTTP(rec, req)

				if rec.Code != tt.want {
					t.Errorf("GET %s = %d, want %d", path, rec.Code, tt.want)
				}
				if tt.want != http.StatusOK && strings.Contains(rec.Body.String(), alice) {
					t.Errorf("GET %s leaked a phone number without the token", path)
				}
			}
		})
	}
}

func TestPublicRoutesStayOpen(t *testing.T) {
	srv := NewServer(config.Config{}, discardLogger(), Deps{})

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200 without a token", rec.Code)
	}
}

// A relay whose send fails with the window open must not vanish.
func TestAFailedRelaySendIsHeld(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	rep.err, rep.failTo = errors.New("graph api 503"), bob

	if !srv.deliver(context.Background(), relayOthers(alice, "call me when you land")) {
		t.Fatalf("deliver reported nothing reached anyone, but the relay was held")
	}

	held, err := srv.deps.Outbox.Take(context.Background(), bob, 10)
	if err != nil || len(held) != 1 || held[0].Body != "call me when you land" {
		t.Fatalf("held for bob = %+v, %v; want the failed relay", held, err)
	}
}

// If nothing got through and nothing was held, the claim must be released so
// Meta's retry can try again, instead of finishing it and dropping the message.
func TestNothingReachedReleasesTheClaim(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	rep.err = errors.New("graph api down")

	srv.handleOnce(context.Background(), "wamid.X", func(context.Context) Reply {
		return replyToSender(alice, "Receipt #1 saved")
	})

	if err := srv.deps.Claims.Claim(context.Background(), "wamid.X"); err != nil {
		t.Fatalf("retry could not claim the message: %v; it was finished with no reply sent", err)
	}
}

func TestAFailedEditOrDeleteAnswersOnlyTheSender(t *testing.T) {
	receipts := &fakeReceipts{
		getReceiptByNumber: func(context.Context, int) (model.Receipt, error) {
			return model.Receipt{}, store.ErrNotFound
		},
	}
	srv := NewServer(config.Config{}, discardLogger(), Deps{
		Receipts: receipts, Relay: relay.New([]string{"+" + alice, "+" + bob}),
	})

	for _, text := range []string{"delete 99", "edit 99 total: 5"} {
		reply := srv.replyToText(context.Background(), inboundText(alice, text))
		if reply.Audience != audienceSender {
			t.Errorf("%q answered audience %v, want the sender alone: nothing changed", text, reply.Audience)
		}
		if !strings.Contains(reply.Body, "#99") {
			t.Errorf("%q answered %q, want the not-found message", text, reply.Body)
		}
	}
}

// A live relay must not overtake older ones still held for the same person.
func TestARelayQueuesBehindAHeldBacklog(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	if _, err := srv.deps.Outbox.Hold(context.Background(), store.HeldMessage{
		To: bob, From: alice, Body: "older", HeldAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	srv.deliver(context.Background(), relayOthers(alice, "newer"))

	for _, m := range rep.sent {
		if m.to == bob {
			t.Fatalf("newer relay went straight to bob ahead of the held backlog")
		}
	}
	held, _ := srv.deps.Outbox.Take(context.Background(), bob, 10)
	if len(held) != 2 || held[0].Body != "older" || held[1].Body != "newer" {
		t.Fatalf("held for bob = %+v, want older then newer", held)
	}
}

func inboundText(from, body string) whatsapp.InboundText {
	return whatsapp.InboundText{From: from, MessageID: "wamid.T", Body: body}
}

func TestATimedOutRequestIsLoggedWithTheStatusTheClientGot(t *testing.T) {
	var logs strings.Builder
	srv := NewServer(config.Config{}, slog.New(slog.NewTextHandler(&logs, nil)), Deps{})

	// The same order as Server.Handler, around a handler that outlives the timeout.
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	handler := httpx.Chain(http.TimeoutHandler(slow, 10*time.Millisecond, "timeout"),
		httpx.RequestID, httpx.Logger(srv.logger))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/whatsapp/webhook", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("client got %d, want 503", rec.Code)
	}
	if !strings.Contains(logs.String(), "status=503") {
		t.Fatalf("log = %q, want status=503", logs.String())
	}
}
