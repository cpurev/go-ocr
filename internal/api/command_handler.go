package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/receipt"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const commandTimeout = 20 * time.Second

type request struct {
	Sender string // normalized digits
	Cmd    Command
}

func (s *Server) replyToText(ctx context.Context, txt whatsapp.InboundText) Reply {
	sender := relay.Normalize(txt.From)

	v, cmd, ok := parseCommand(txt.Body)
	if !ok {
		s.logger.Info("webhook text message was not a command",
			"from", txt.From, "message_id", txt.MessageID, "body_bytes", len(txt.Body))

		return relayOthers(sender, txt.Body)
	}

	// A parse error and a missing dependency are between the bot and whoever
	// typed, so they go to the sender alone whatever the verb's row broadcasts.
	if missing := s.missing(v.Needs); missing != "" {
		return replyToSender(sender, missing)
	}
	if cmd.Err != nil {
		return replyToSender(sender,
			fmt.Sprintf("I couldn't read that: %s\n\nTry: %s", cmd.Err, v.Usage))
	}

	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	return Reply{
		Sender:   sender,
		Audience: v.Audience,
		Body:     v.Run(s, ctx, request{Sender: sender, Cmd: cmd}),
	}
}

// missing names the dependency a verb needs and this deployment does not have.
func (s *Server) missing(n need) string {
	if n&needsReceipts != 0 && s.deps.Receipts == nil {
		return "That needs the database, which isn't configured on this server."
	}
	if n&needsStores != 0 && s.deps.Stores == nil {
		return "The store directory isn't configured on this server."
	}
	return ""
}

func (s *Server) helpReply(ctx context.Context, req request) string { return helpText }

func (s *Server) editReply(ctx context.Context, req request) string {
	existing, err := s.deps.Receipts.GetReceiptByNumber(ctx, req.Cmd.Number)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Sprintf("I don't have a receipt #%d.", req.Cmd.Number)
	}
	if err != nil {
		s.logger.Error("looking up receipt for edit", "number", req.Cmd.Number, "error", err)
		return "Something went wrong finding that receipt. Please try again."
	}

	if problems := req.Cmd.Update.Validate(); len(problems) > 0 {
		var b strings.Builder
		b.WriteString("That edit isn't valid:\n")
		for field, problem := range problems {
			fmt.Fprintf(&b, "• %s %s\n", field, problem)
		}
		return b.String()
	}

	updated, err := s.deps.Receipts.UpdateReceipt(ctx, existing.ID, req.Cmd.Update)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Sprintf("I don't have a receipt #%d.", req.Cmd.Number)
	}
	if err != nil {
		s.logger.Error("updating receipt", "number", req.Cmd.Number, "error", err)
		return "Something went wrong saving that edit. Please try again."
	}

	s.logger.Info("receipt edited",
		"number", updated.Number, "receipt_id", updated.ID,
		"merchant", updated.Merchant, "total", updated.Total)

	learned := s.teachStore(ctx, existing, req.Cmd.Update)

	return formatEditReply(updated, learned)
}

func (s *Server) teachStore(ctx context.Context, existing model.Receipt, update model.ReceiptUpdate) string {
	if s.deps.Stores == nil || update.Merchant == nil {
		return ""
	}

	merchant := strings.TrimSpace(*update.Merchant)
	if merchant == "" {
		return ""
	}

	orgNr := receipt.FindOrgNr(existing.RawText)
	if orgNr == "" {
		s.logger.Info("no registration number on receipt, nothing to learn",
			"number", existing.Number)
		return ""
	}

	if _, err := s.deps.Stores.SaveStore(ctx, orgNr, merchant); err != nil {
		s.logger.Error("saving store mapping",
			"org_nr", orgNr, "merchant", merchant, "error", err)
		return ""
	}

	s.logger.Info("learned store", "org_nr", orgNr, "merchant", merchant)
	return merchant
}

func (s *Server) storesReply(ctx context.Context, req request) string {
	stores, err := s.deps.Stores.ListStores(ctx)
	if err != nil {
		s.logger.Error("listing stores", "error", err)
		return "Something went wrong reading the store list."
	}
	if len(stores) == 0 {
		return "I haven't learned any shops yet.\n\n" +
			"Correct one with `edit 7 merchant: ICA` and I'll remember it."
	}

	var b strings.Builder
	b.WriteString("*Shops I know*\n\n")
	for _, st := range stores {
		fmt.Fprintf(&b, "%s (%s)\n", st.Merchant, st.OrgNr)
	}
	return b.String()
}

func formatEditReply(r model.Receipt, learned string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "*Receipt #%d updated*\n\n", r.Number)

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

	if learned != "" {
		fmt.Fprintf(&b, "\nI'll call this shop %s from now on.", learned)
	}

	return b.String()
}
