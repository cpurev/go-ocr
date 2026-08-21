package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/httpx"
	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/ocr"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const webhookMaxBody = 1 << 20

func (s *Server) handleWebhookVerify(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WhatsAppVerifyToken == "" {
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"the WhatsApp webhook is not configured on this server", nil)
		return
	}

	q := r.URL.Query()
	token := q.Get("hub.verify_token")

	if q.Get("hub.mode") != "subscribe" ||
		subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.WhatsAppVerifyToken)) != 1 {
		httpx.Error(w, r, http.StatusForbidden, "verify token mismatch", nil)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(q.Get("hub.challenge"))); err != nil {
		s.logger.ErrorContext(r.Context(), "writing webhook challenge", "error", err)
	}
}

func (s *Server) handleWebhookReceive(w http.ResponseWriter, r *http.Request) {
	if s.cfg.WhatsAppAppSecret == "" {
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"the WhatsApp webhook is not configured on this server", nil)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webhookMaxBody))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "unreadable request body", nil)
		return
	}

	if !whatsapp.VerifySignature(s.cfg.WhatsAppAppSecret, body, r.Header.Get(whatsapp.SignatureHeader)) {
		s.logger.WarnContext(r.Context(), "webhook signature verification failed",
			"request_id", httpx.RequestIDFrom(r.Context()))
		httpx.Error(w, r, http.StatusForbidden, "invalid signature", nil)
		return
	}

	var n whatsapp.Notification
	if err := json.Unmarshal(body, &n); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "malformed notification payload", nil)
		return
	}

	// Handled inline, not in goroutines: Cloud Run throttles CPU once the
	// response is written. Retries are safe, whatsappMediaId is unique.
	images := n.Images()
	for _, img := range images {
		s.ingestInboundImage(r.Context(), img)
	}

	for _, txt := range n.Texts() {
		s.handleTextCommand(r.Context(), txt)
	}

	httpx.OK(w, http.StatusOK, map[string]int{"images_accepted": len(images)}, nil)
}

func (s *Server) ingestInboundImage(ctx context.Context, img whatsapp.InboundImage) {
	defer func() {
		if p := recover(); p != nil {
			s.logger.Error("panic while ingesting webhook image",
				"media_id", img.MediaID, "panic", p)
		}
	}()

	budget := s.cfg.WhatsAppTimeout + s.cfg.OCRTimeout + s.cfg.MongoTimeout
	if budget <= 0 {
		budget = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	created, err := s.deps.Ingester.Ingest(ctx, model.ReceiptInput{
		WhatsAppMediaID: img.MediaID,
		UserID:          img.From,
		GroupID:         img.GroupID,
	})

	var reply string
	switch {
	case errors.Is(err, store.ErrDuplicate):

		s.logger.Info("webhook image already ingested", "media_id", img.MediaID)
		reply = "I already have that receipt saved, nothing new to add."

	case errors.Is(err, whatsapp.ErrMediaNotFound):

		s.logger.Warn("webhook image expired before download",
			"media_id", img.MediaID, "error", err)
		reply = "That image expired before I could fetch it. Please send it again."

	case errors.Is(err, whatsapp.ErrNotImage):
		s.logger.Info("webhook media was not an image", "media_id", img.MediaID)
		reply = "I can only read photos. That looked like a video or a document."

	case errors.Is(err, ocr.ErrUnreadable):
		s.logger.Info("webhook image unreadable", "media_id", img.MediaID)
		reply = "I couldn't find any text in that photo. Try again with more light, " +
			"the receipt flat, and the whole thing in frame."

	case err != nil:
		s.logger.Error("webhook image ingestion failed",
			"media_id", img.MediaID, "from", img.From, "error", err)
		reply = "Something went wrong on my side reading that receipt. Please try again."

	default:
		s.logger.Info("webhook image ingested",
			"receipt_id", created.ID, "media_id", img.MediaID,
			"merchant", created.Merchant, "total", created.Total, "date", created.Date)
		reply = formatReceiptReply(created)
	}

	s.broadcast(img.From, img.GroupID, reply)
}

const replyTimeout = 15 * time.Second

// broadcast sends body to the sender as written and to everyone else on the
// roster with attribution. Off-roster senders get a plain 1:1 reply.
func (s *Server) broadcast(sender, groupID, body string) {
	if body == "" {
		return
	}

	s.replyTo(sender, groupID, body)

	for _, other := range s.deps.Relay.Others(sender) {
		s.replyTo(other, "", attribute(sender, body))
	}
}

// forward relays a message to the rest of the roster, skipping the sender.
func (s *Server) forward(sender, body string) {
	if body == "" {
		return
	}

	for _, other := range s.deps.Relay.Others(sender) {
		s.replyTo(other, "", attribute(sender, body))
	}
}

// attribute labels a relayed message with who sent it.
func attribute(sender, body string) string {
	return "From +" + relay.Normalize(sender) + ":\n\n" + body
}

// replyTo answers the group when groupID is set, otherwise the sender.
func (s *Server) replyTo(to, groupID, body string) {
	if s.deps.Replier == nil || body == "" {
		return
	}
	if groupID == "" && to == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), replyTimeout)
	defer cancel()

	dest := to
	var err error
	if groupID != "" {
		dest = groupID
		err = s.deps.Replier.SendGroupText(ctx, groupID, body)
	} else {
		err = s.deps.Replier.SendText(ctx, to, body)
	}

	switch {
	case errors.Is(err, whatsapp.ErrOutsideWindow):
		s.logger.Warn("whatsapp reply dropped: recipient's 24h window is closed",
			"to", dest, "hint", "recipient must message the bot to reopen it")
		return
	case err != nil:
		s.logger.Error("sending whatsapp reply",
			"to", dest, "group", groupID != "", "error", err)
		return
	}
	s.logger.Info("whatsapp reply sent",
		"to", dest, "group", groupID != "", "body_bytes", len(body))
}

func formatReceiptReply(r model.Receipt) string {
	var b strings.Builder

	if r.Number > 0 {
		fmt.Fprintf(&b, "*Receipt #%d saved*\n\n", r.Number)
	} else {
		b.WriteString("*Receipt saved*\n\n")
	}

	if r.Merchant != "" {
		fmt.Fprintf(&b, "Merchant: %s\n", r.Merchant)
	}
	if r.Date != "" {
		fmt.Fprintf(&b, "Date: %s\n", r.Date)
	}
	fmt.Fprintf(&b, "Total: %.2f %s\n", r.Total, r.Currency)
	if r.Tax > 0 {
		fmt.Fprintf(&b, "Tax: %.2f %s\n", r.Tax, r.Currency)
	}
	if n := len(r.LineItems); n > 0 {
		fmt.Fprintf(&b, "Items: %d\n", n)
	}

	if r.Total == 0 {
		b.WriteString("\nI couldn't make out the total on this one. " +
			"the text was there but no amount matched.\n")
	}

	if r.Number > 0 {
		fmt.Fprintf(&b, "\nWrong? edit %d merchant: ICA", r.Number)
	} else {
		fmt.Fprintf(&b, "\nid: %s", r.ID)
	}

	return b.String()
}
