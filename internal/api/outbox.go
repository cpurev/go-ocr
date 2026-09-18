package api

import (
	"context"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const (
	// serviceWindow is how long after a person's last message WhatsApp lets
	// the business send them free-form text. After it, Meta accepts the send
	// with a 200 and then fails it with 131047, which is how relays were lost.
	serviceWindow = 24 * time.Hour

	// windowMargin treats the window as closed a little early, so a send in
	// flight as it closes is held rather than failed on the far side of it.
	windowMargin = 10 * time.Minute

	// maxFlush bounds how many held relays one inbound message delivers, since
	// each is an inline send. The rest go out on that member's next message.
	maxFlush = 20
)

// storeBudget bounds one outbox or claim call.
func (s *Server) storeBudget() time.Duration {
	if s.cfg.MongoTimeout > 0 {
		return s.cfg.MongoTimeout
	}
	return defaultClaimTimeout
}

// windowOpen reports whether number wrote to the bot recently enough to be
// sent free-form text. A number never seen counts as closed. An outbox that
// cannot answer counts as open, which is how the bot behaved before holding.
func (s *Server) windowOpen(ctx context.Context, number string) bool {
	ctx, cancel := context.WithTimeout(ctx, s.storeBudget())
	defer cancel()

	last, err := s.deps.Outbox.LastSeen(ctx, number)
	if err != nil {
		s.logger.Warn("reading service window failed, sending anyway",
			"to", number, "error", err)
		return true
	}
	return !last.IsZero() && time.Since(last) < serviceWindow-windowMargin
}

// hold queues a relay and returns how many now wait for its recipient.
func (s *Server) hold(ctx context.Context, m store.HeldMessage) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, s.storeBudget())
	defer cancel()

	pending, err := s.deps.Outbox.Hold(ctx, m)
	if err != nil {
		s.logger.Error("holding relay for a closed window", "to", m.To, "error", err)
		return 0, err
	}
	s.logger.Info("relay held: recipient's 24h window is closed",
		"to", m.To, "from", m.From, "pending", pending, "body_bytes", len(m.Body))
	return pending, nil
}

// recordSenders notes who just wrote, which reopens their 24-hour window, and
// hands them whatever the relay held while it was closed. It runs before the
// new message is handled so held relays arrive in the order they were sent.
func (s *Server) recordSenders(ctx context.Context, n whatsapp.Notification) {
	if !s.deps.Relay.Active() {
		return
	}
	ctx = context.WithoutCancel(ctx)

	for _, sender := range n.Senders(time.Now()) {
		from := relay.Normalize(sender.From)
		if !s.deps.Relay.Has(from) {
			continue
		}

		seenCtx, cancel := context.WithTimeout(ctx, s.storeBudget())
		err := s.deps.Outbox.Seen(seenCtx, from, sender.At)
		cancel()
		if err != nil {
			s.logger.Error("recording service window", "from", from, "error", err)
		}

		s.flushHeld(ctx, from)
	}
}

// flushHeld delivers relays held for to. Take removes each one before it is
// sent, so an instance killed mid-send loses that message; the alternative, a
// lease, is not worth it for a two-phone relay.
func (s *Server) flushHeld(ctx context.Context, to string) {
	if s.deps.Replier == nil {
		return
	}

	takeCtx, cancel := context.WithTimeout(ctx, s.storeBudget())
	held, err := s.deps.Outbox.Take(takeCtx, to, maxFlush)
	cancel()
	if err != nil {
		s.logger.Error("taking held relays", "to", to, "error", err)
	}
	if len(held) == 0 {
		return
	}

	delivered := 0
	for _, h := range held {
		if err := s.trySend(ctx, h.To, s.attributeHeld(h)); err != nil {
			// Back in the queue for the next time they write.
			_, _ = s.hold(ctx, h)
			continue
		}
		delivered++
	}
	s.logger.Info("held relays delivered", "to", to, "delivered", delivered, "taken", len(held))
}

// attributeHeld labels a late relay with who sent it and when, since it may
// arrive days after the conversation it belonged to.
func (s *Server) attributeHeld(h store.HeldMessage) string {
	when := h.HeldAt.In(s.cfg.Location()).Format("Mon 2 Jan 15:04")
	return "From +" + relay.Normalize(h.From) + " (sent " + when + "):\n\n" + h.Body
}

// heldNotice tells a sender whose relay is waiting on someone else's window.
func heldNotice(waiting []string) string {
	if len(waiting) == 0 {
		return ""
	}

	names := make([]string, len(waiting))
	for i, n := range waiting {
		names[i] = "+" + n
	}
	who, verb := strings.Join(names, " and "), "hasn't"
	if len(waiting) > 1 {
		verb = "haven't"
	}

	return who + " " + verb + " messaged me in the last 24 hours, so WhatsApp won't let me " +
		"pass this on yet. It's saved and goes through as soon as they send me anything."
}

func withNotice(body, notice string) string {
	if notice == "" {
		return body
	}
	return body + "\n\n_" + notice + "_"
}
