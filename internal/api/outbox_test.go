package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

// postText drives the real webhook handler with one signed text message.
func postText(t *testing.T, srv *Server, from, id string, sentAt time.Time, text string) {
	t.Helper()

	body := `{"object":"whatsapp_business_account","entry":[{"id":"E","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","messages":[{"from":"` + from + `","id":"` + id + `","timestamp":"` + strconv.FormatInt(sentAt.Unix(), 10) + `","type":"text","text":{"body":"` + text + `"}}]}}]}]}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/whatsapp/webhook", strings.NewReader(body))
	req.Header.Set(whatsapp.SignatureHeader, sign(t, body))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

func newWebhookServer(t *testing.T) (*Server, *fakeReplier) {
	t.Helper()

	rep := &fakeReplier{}
	srv := NewServer(
		config.Config{WhatsAppAppSecret: reproSecret},
		discardLogger(),
		Deps{Replier: rep, Relay: relay.New([]string{"+" + alice, "+" + bob})},
	)
	return srv, rep
}

// The production failure: bob last wrote two days ago, so a relay to him is
// accepted by Meta and then dropped with 131047. It must be held instead.
func TestRelayToAClosedWindowIsHeldAndTheSenderIsTold(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	srv.deps.Outbox = newOutboxSeenAt(t, map[string]time.Time{
		alice: time.Now(),
		bob:   time.Now().Add(-47 * time.Hour),
	})

	srv.deliver(context.Background(), replyAll(alice, "Receipt #12 saved"))

	if got := rep.recipients(); len(got) != 1 || got[0] != alice {
		t.Fatalf("sent to %v, want only %s: bob's window is closed", got, alice)
	}
	if !strings.HasPrefix(rep.sent[0].body, "Receipt #12 saved") ||
		!strings.Contains(rep.sent[0].body, "+"+bob+" hasn't messaged me in the last 24 hours") {
		t.Errorf("sender got %q, want the reply plus a note that bob's copy is waiting", rep.sent[0].body)
	}

	held, err := srv.deps.Outbox.Take(context.Background(), bob, 10)
	if err != nil || len(held) != 1 || held[0].Body != "Receipt #12 saved" || held[0].From != alice {
		t.Fatalf("held for bob = %+v, %v; want the one relay from alice", held, err)
	}
}

func TestSenderIsToldOnlyAboutTheFirstHeldRelay(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	srv.deps.Outbox = newOutboxSeenAt(t, map[string]time.Time{alice: time.Now()})

	srv.deliver(context.Background(), relayOthers(alice, "picking up milk"))
	srv.deliver(context.Background(), relayOthers(alice, "and eggs"))

	if got := len(rep.sent); got != 1 {
		t.Fatalf("sent %d messages, want 1 notice for two held relays: %v", got, rep.sent)
	}
	if rep.sent[0].to != alice {
		t.Errorf("notice went to %s, want the sender %s", rep.sent[0].to, alice)
	}
}

func TestWindowJustUnder24HoursIsTreatedAsClosed(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	srv.deps.Outbox = newOutboxSeenAt(t, map[string]time.Time{
		alice: time.Now(),
		bob:   time.Now().Add(-serviceWindow + windowMargin/2),
	})

	srv.deliver(context.Background(), relayOthers(alice, "hi"))

	for _, m := range rep.sent {
		if m.to == bob {
			t.Fatalf("relayed to bob inside the margin, where the send could fail in flight")
		}
	}
}

// Writing anything reopens the window, so held relays go out first, labelled
// with when they were sent, and later relays go straight through.
func TestHeldRelaysAreDeliveredWhenTheRecipientWrites(t *testing.T) {
	srv, rep := newWebhookServer(t)
	seenAt(t, srv, time.Now(), alice)

	postText(t, srv, alice, "wamid.A1", time.Now(), "picking up milk")
	if got := len(rep.sent); got != 1 || rep.sent[0].to != alice {
		t.Fatalf("first relay sent %v, want only the notice to alice", rep.recipients())
	}
	rep.sent = nil

	postText(t, srv, bob, "wamid.B1", time.Now(), "ok thanks")

	if got := rep.recipients(); len(got) != 2 || got[0] != bob || got[1] != alice {
		t.Fatalf("sent to %v, want the held relay to bob, then bob's chat to alice", got)
	}
	if want := "From +" + alice + " (sent "; !strings.HasPrefix(rep.sent[0].body, want) ||
		!strings.HasSuffix(rep.sent[0].body, ":\n\npicking up milk") {
		t.Errorf("held relay body %q, want it attributed with its send time", rep.sent[0].body)
	}
	rep.sent = nil

	postText(t, srv, alice, "wamid.A2", time.Now(), "see you soon")
	if got := rep.recipients(); len(got) != 1 || got[0] != bob {
		t.Fatalf("sent to %v, want a direct relay to bob now that his window is open", got)
	}
}

func TestHeldRelaysAreNotDeliveredTwiceOnAWebhookRetry(t *testing.T) {
	srv, rep := newWebhookServer(t)
	seenAt(t, srv, time.Now(), alice)
	srv.deliver(context.Background(), relayOthers(alice, "picking up milk"))
	rep.sent = nil

	postText(t, srv, bob, "wamid.B1", time.Now(), "ok")
	postText(t, srv, bob, "wamid.B1", time.Now(), "ok")

	count := 0
	for _, m := range rep.sent {
		if m.to == bob {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("bob got %d copies of the held relay, want 1", count)
	}
}

func TestAFailedFlushKeepsTheRelayHeld(t *testing.T) {
	srv, rep := newWebhookServer(t)
	seenAt(t, srv, time.Now(), alice)
	srv.deliver(context.Background(), relayOthers(alice, "picking up milk"))

	rep.err, rep.failTo = errors.New("graph api down"), bob
	postText(t, srv, bob, "wamid.B1", time.Now(), "ok")

	held, err := srv.deps.Outbox.Take(context.Background(), bob, 10)
	if err != nil || len(held) != 1 || held[0].Body != "picking up milk" {
		t.Fatalf("held for bob = %+v, %v; want the relay back in the outbox", held, err)
	}
}

// A retry of an old webhook must not make a stale window look fresh.
func TestWindowCountsFromWhenTheMessageWasSent(t *testing.T) {
	srv, rep := newWebhookServer(t)

	postText(t, srv, bob, "wamid.OLD", time.Now().Add(-30*time.Hour), "old")
	rep.sent = nil

	srv.deliver(context.Background(), relayOthers(alice, "hi"))
	for _, m := range rep.sent {
		if m.to == bob {
			t.Fatalf("relayed to bob off a 30 hour old message")
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newOutboxSeenAt builds an outbox where each number last wrote at the given
// time. A number left out has never written.
func newOutboxSeenAt(t *testing.T, seen map[string]time.Time) *store.MemoryOutbox {
	t.Helper()

	o := store.NewMemoryOutbox()
	for n, at := range seen {
		if err := o.Seen(context.Background(), n, at); err != nil {
			t.Fatalf("seeding %s: %v", n, err)
		}
	}
	return o
}
