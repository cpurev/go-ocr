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

func replyToSender(sender, body string) Reply {
	return Reply{Sender: sender, Audience: audienceSender, Body: body}
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

	claimCtx, cancel := context.WithTimeout(ctx, s.storeBudget())
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

// deliver is the only place in the server that sends a WhatsApp message in
// answer to one. A roster member whose 24-hour window is closed would lose the
// relay, so theirs is held for later and the sender is told once.
func (s *Server) deliver(ctx context.Context, r Reply) {
	if r.Silent() || s.deps.Replier == nil {
		return
	}

	// The reply outlives the request on purpose. Meta has usually given up on
	// this delivery already, and a cancelled request must not take the answer
	// with it.
	ctx = context.WithoutCancel(ctx)

	var reach, waiting []string
	if r.Audience != audienceSender {
		for _, other := range s.deps.Relay.Others(r.Sender) {
			if s.windowOpen(ctx, other) {
				reach = append(reach, other)
				continue
			}
			pending, err := s.hold(ctx, store.HeldMessage{
				To: other, From: r.Sender, Body: r.Body, HeldAt: time.Now(),
			})
			if err != nil {
				// Trying beats keeping it nowhere; Meta may still take it.
				reach = append(reach, other)
				continue
			}
			if pending == 1 {
				waiting = append(waiting, other)
			}
		}
	}

	notice := heldNotice(waiting)
	switch {
	case r.Audience != audienceOthers:
		s.send(ctx, r.Sender, withNotice(r.Body, notice))
	case notice != "":
		s.send(ctx, r.Sender, notice)
	}

	for _, other := range reach {
		s.send(ctx, other, attribute(r.Sender, r.Body))
	}
}

// send posts one message and logs the outcome. It never returns an error
// because one recipient failing must not stop the fan-out.
func (s *Server) send(ctx context.Context, to, body string) {
	_ = s.trySend(ctx, to, body)
}

// trySend posts one message, logs the outcome, and reports it to callers that
// can do something about a failure.
func (s *Server) trySend(ctx context.Context, to, body string) error {
	if to == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, replyTimeout)
	defer cancel()

	messageID, err := s.deps.Replier.SendText(ctx, to, body)
	switch {
	case errors.Is(err, whatsapp.ErrOutsideWindow):
		s.logger.Warn("whatsapp reply dropped: recipient's 24h window is closed",
			"to", to, "hint", "recipient must message the bot to reopen it")
		return err
	case err != nil:
		s.logger.Error("sending whatsapp reply", "to", to, "error", err)
		return err
	}
	// Accepted, not delivered: a later failed status carries this message_id.
	s.logger.Info("whatsapp reply sent", "to", to, "message_id", messageID, "body_bytes", len(body))
	return nil
}

// attribute labels a relayed message with who sent it.
func attribute(sender, body string) string {
	return "From +" + relay.Normalize(sender) + ":\n\n" + body
}
