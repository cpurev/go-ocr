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
	// response is written. That makes this slower than Meta's webhook timeout,
	// so retries are the normal path and handleOnce turns them into no-ops.
	images := n.Images()
	for _, img := range images {
		s.handleOnce(r.Context(), img.MessageID, func(ctx context.Context) Reply {
			return s.replyToImage(ctx, img)
		})
	}

	for _, txt := range n.Texts() {
		s.handleOnce(r.Context(), txt.MessageID, func(ctx context.Context) Reply {
			return s.replyToText(ctx, txt)
		})
	}

	httpx.OK(w, http.StatusOK, map[string]int{"images_accepted": len(images)}, nil)
}

func (s *Server) replyToImage(ctx context.Context, img whatsapp.InboundImage) Reply {
	budget := s.cfg.WhatsAppTimeout + s.cfg.OCRTimeout + s.cfg.MongoTimeout
	if budget <= 0 {
		budget = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	created, err := s.deps.Ingester.Ingest(ctx, model.ReceiptInput{
		WhatsAppMediaID: img.MediaID,
		UserID:          img.From,
	})

	sender := relay.Normalize(img.From)

	switch {
	case errors.Is(err, store.ErrDuplicate):

		s.logger.Info("webhook image already ingested", "media_id", img.MediaID)
		return replyAll(sender, "I already have that receipt saved, nothing new to add.")

	case errors.Is(err, whatsapp.ErrMediaNotFound):

		s.logger.Warn("webhook image expired before download",
			"media_id", img.MediaID, "error", err)
		return replyAll(sender, "That image expired before I could fetch it. Please send it again.")

	case errors.Is(err, whatsapp.ErrNotImage):
		s.logger.Info("webhook media was not an image", "media_id", img.MediaID)
		return replyAll(sender, "I can only read photos. That looked like a video or a document.")

	case errors.Is(err, ocr.ErrUnreadable):
		s.logger.Info("webhook image unreadable", "media_id", img.MediaID)
		return replyAll(sender, "I couldn't find any text in that photo. Try again with more light, "+
			"the receipt flat, and the whole thing in frame.")

	case err != nil:
		s.logger.Error("webhook image ingestion failed",
			"media_id", img.MediaID, "from", img.From, "error", err)
		return replyAll(sender, "Something went wrong on my side reading that receipt. Please try again.")

	default:
		s.logger.Info("webhook image ingested",
			"receipt_id", created.ID, "media_id", img.MediaID,
			"merchant", created.Merchant, "total", created.Total, "date", created.Date)
		return replyAll(sender, formatReceiptReply(created))
	}
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
