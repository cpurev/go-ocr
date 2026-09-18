package api

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

// fakeReplier records every send so tests can assert on fan-out.
type fakeReplier struct {
	mu   sync.Mutex
	sent []sentMessage
	err  error

	// failTo, when set, makes sends to that one number fail with err.
	failTo string
}

type sentMessage struct {
	to   string
	body string
}

func (f *fakeReplier) SendText(_ context.Context, to, body string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{to: to, body: body})
	if f.failTo != "" && to != f.failTo {
		return "wamid.FAKE", nil
	}
	if f.err != nil {
		return "", f.err
	}
	return "wamid.FAKE", nil
}

func (f *fakeReplier) recipients() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, m := range f.sent {
		out = append(out, m.to)
	}
	return out
}

const (
	alice    = "97611111111"
	bob      = "97622222222"
	stranger = "97699999999"
)

// newTestServer builds a server whose roster members all wrote to the bot a
// minute ago, so every 24-hour window is open unless a test closes one.
func newTestServer(t *testing.T, numbers []string) (*Server, *fakeReplier) {
	t.Helper()

	rep := &fakeReplier{}
	srv := NewServer(
		config.Config{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps{Replier: rep, Relay: relay.New(numbers)},
	)
	seenAt(t, srv, time.Now().Add(-time.Minute), srv.deps.Relay.Members()...)
	return srv, rep
}

// seenAt records numbers as having last written to the bot at at.
func seenAt(t *testing.T, srv *Server, at time.Time, numbers ...string) {
	t.Helper()

	for _, n := range numbers {
		if err := srv.deps.Outbox.Seen(context.Background(), n, at); err != nil {
			t.Fatalf("seeding %s as seen: %v", n, err)
		}
	}
}

func TestBroadcastFansOutToRoster(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})

	srv.deliver(context.Background(), replyAll(alice, "Receipt #12 saved"))

	if got := len(rep.sent); got != 2 {
		t.Fatalf("sent %d messages, want 2 (sender + one other): %v", got, rep.recipients())
	}

	if rep.sent[0].to != alice || rep.sent[0].body != "Receipt #12 saved" {
		t.Errorf("sender got %+v, want unattributed body to %s", rep.sent[0], alice)
	}

	want := "From +" + alice + ":\n\nReceipt #12 saved"
	if rep.sent[1].to != bob || rep.sent[1].body != want {
		t.Errorf("other got %+v, want attributed body to %s", rep.sent[1], bob)
	}
}

func TestBroadcastFromStrangerStaysPrivate(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})

	srv.deliver(context.Background(), replyAll(stranger, "hello?"))

	if got := len(rep.sent); got != 1 {
		t.Fatalf("sent %d messages, want 1: %v", got, rep.recipients())
	}
	if rep.sent[0].to != stranger {
		t.Errorf("replied to %s, want only the stranger %s", rep.sent[0].to, stranger)
	}
}

func TestForwardSkipsSender(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})

	srv.deliver(context.Background(), relayOthers(alice, "picking up milk"))

	if got := len(rep.sent); got != 1 {
		t.Fatalf("sent %d messages, want 1: %v", got, rep.recipients())
	}
	if rep.sent[0].to != bob {
		t.Errorf("forwarded to %s, want %s", rep.sent[0].to, bob)
	}
	if want := "From +" + alice + ":\n\npicking up milk"; rep.sent[0].body != want {
		t.Errorf("body = %q, want %q", rep.sent[0].body, want)
	}
}

func TestNoRosterCollapsesToDirectReply(t *testing.T) {
	srv, rep := newTestServer(t, nil)

	srv.deliver(context.Background(), replyAll(alice, "Receipt #12 saved"))

	if got := len(rep.sent); got != 1 {
		t.Fatalf("sent %d messages, want 1: %v", got, rep.recipients())
	}
	if rep.sent[0].to != alice {
		t.Errorf("got %+v, want a plain 1:1 reply to %s", rep.sent[0], alice)
	}
}

func TestClosedWindowDoesNotHaltFanOut(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})
	rep.err = whatsapp.ErrOutsideWindow

	srv.deliver(context.Background(), replyAll(alice, "Receipt #12 saved"))

	// Both sends are still attempted; the closed window is logged, not fatal.
	if got := len(rep.sent); got != 2 {
		t.Fatalf("sent %d messages, want 2 attempts despite the closed window", got)
	}
}

func TestEmptyBodyIsNotSent(t *testing.T) {
	srv, rep := newTestServer(t, []string{"+" + alice, "+" + bob})

	srv.deliver(context.Background(), replyAll(alice, ""))
	srv.deliver(context.Background(), relayOthers(alice, ""))

	if got := len(rep.sent); got != 0 {
		t.Fatalf("sent %d messages, want 0: %v", got, rep.recipients())
	}
}
