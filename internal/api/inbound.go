package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
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

// defaultClaimTimeout bounds the claim write when MONGO_TIMEOUT is unset.
const defaultClaimTimeout = 10 * time.Second

// handleOnce runs work at most once per WhatsApp message id and delivers what
// it decided to say. Meta redelivers any webhook it did not get a fast 200 for,
// and the image path cannot answer inside that window, so the claim is the only
// thing between a retry and a second reply.
func (s *Server) handleOnce(ctx context.Context, messageID string, work func(context.Context) Reply) {
	defer func() {
		if p := recover(); p != nil {
			s.logger.Error("panic while handling inbound message",
				"message_id", messageID, "panic", p)
			if messageID != "" {
				s.release(context.WithoutCancel(ctx), messageID)
			}
		}
	}()

	if messageID == "" {
		s.deliver(ctx, work(ctx))
		return
	}

	budget := s.cfg.MongoTimeout
	if budget <= 0 {
		budget = defaultClaimTimeout
	}
	claimCtx, cancel := context.WithTimeout(ctx, budget)
	err := s.deps.Claims.Claim(claimCtx, messageID)
	cancel()

	switch {
	case errors.Is(err, store.ErrClaimed):
		s.logger.Info("webhook redelivery ignored", "message_id", messageID)
		return
	case err != nil:
		// A claim store outage must not silence the bot. Working unclaimed
		// risks the duplicate we are fixing, which beats losing the message.
		s.logger.Warn("claiming message id failed, proceeding unclaimed",
			"message_id", messageID, "error", err)
	}

	reply := work(ctx)
	if reply.Silent() {
		// Nothing user-visible happened, so a retry is free to try again.
		s.release(ctx, messageID)
		return
	}

	s.deliver(ctx, reply)

	if err := s.deps.Claims.Finish(context.WithoutCancel(ctx), messageID); err != nil {
		s.logger.Error("finishing message claim", "message_id", messageID, "error", err)
	}
}

func (s *Server) release(ctx context.Context, messageID string) {
	if err := s.deps.Claims.Release(ctx, messageID); err != nil {
		s.logger.Error("releasing message claim", "message_id", messageID, "error", err)
	}
}

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
