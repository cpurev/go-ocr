package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const replyTimeout = 15 * time.Second

// Audience says who a Reply reaches.
type Audience uint8

const (
	// audienceSender answers only the phone that asked. Queries use it so one
	// person reading their own total does not buzz the other's phone.
	audienceSender Audience = iota

	// audienceEveryone answers the sender verbatim and every other roster
	// member with attribution. Anything that changed shared state uses it.
	audienceEveryone

	// audienceOthers relays to the rest of the roster only. Plain chat uses it.
	audienceOthers
)

// Reply is what one inbound message decided to say. Body is the single source
// of truth for whether anything gets sent, so there is no state where an
// audience is set but nothing goes out.
type Reply struct {
	Sender   string
	Audience Audience
	Body     string
}

func replyAll(sender, body string) Reply {
	return Reply{Sender: sender, Audience: audienceEveryone, Body: body}
}

func relayOthers(sender, body string) Reply {
	return Reply{Sender: sender, Audience: audienceOthers, Body: body}
}

// Silent reports a Reply that says nothing. The zero Reply is silent.
func (r Reply) Silent() bool { return strings.TrimSpace(r.Body) == "" }

// deliver is the only place in the server that sends a WhatsApp message.
func (s *Server) deliver(ctx context.Context, r Reply) {
	if r.Silent() || s.deps.Replier == nil {
		return
	}

	// The reply outlives the request on purpose. Meta has usually given up on
	// this delivery already, and a cancelled request must not take the answer
	// with it.
	ctx = context.WithoutCancel(ctx)

	if r.Audience != audienceOthers {
		s.send(ctx, r.Sender, r.Body)
	}
	if r.Audience != audienceSender {
		for _, other := range s.deps.Relay.Others(r.Sender) {
			s.send(ctx, other, attribute(r.Sender, r.Body))
		}
	}
}

// send posts one message and classifies the outcome. It never returns an error
// because a closed 24-hour window on one recipient must not stop the fan-out.
func (s *Server) send(ctx context.Context, to, body string) {
	if to == "" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, replyTimeout)
	defer cancel()

	err := s.deps.Replier.SendText(ctx, to, body)
	switch {
	case errors.Is(err, whatsapp.ErrOutsideWindow):
		s.logger.Warn("whatsapp reply dropped: recipient's 24h window is closed",
			"to", to, "hint", "recipient must message the bot to reopen it")
		return
	case err != nil:
		s.logger.Error("sending whatsapp reply", "to", to, "error", err)
		return
	}
	s.logger.Info("whatsapp reply sent", "to", to, "body_bytes", len(body))
}

// attribute labels a relayed message with who sent it.
func attribute(sender, body string) string {
	return "From +" + relay.Normalize(sender) + ":\n\n" + body
}
