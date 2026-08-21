package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/receipt"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const commandTimeout = 20 * time.Second

func (s *Server) handleTextCommand(ctx context.Context, txt whatsapp.InboundText) {
	defer func() {
		if p := recover(); p != nil {
			s.logger.Error("panic while handling text command",
				"message_id", txt.MessageID, "panic", p)
		}
	}()

	cmd := ParseCommand(txt.Body)
	if cmd.Kind == CommandNone {
		s.logger.Info("webhook text message was not a command",
			"from", txt.From, "message_id", txt.MessageID, "body_bytes", len(txt.Body))

		s.forward(txt.From, txt.Body)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var reply string
	switch cmd.Kind {
	case CommandHelp:
		reply = HelpText
	case CommandStores:
		reply = s.listStoresReply(ctx)
	case CommandEdit:
		reply = s.editReceiptReply(ctx, cmd)
	}

	s.broadcast(txt.From, txt.GroupID, reply)
}

func (s *Server) editReceiptReply(ctx context.Context, cmd Command) string {
	if cmd.Err != nil {
		return fmt.Sprintf("I couldn't read that edit: %s\n\nTry: edit 7 merchant: ICA", cmd.Err)
	}
	if s.deps.Receipts == nil {
		return "Editing needs the database, which isn't configured on this server."
	}

	existing, err := s.deps.Receipts.GetReceiptByNumber(ctx, cmd.Number)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Sprintf("I don't have a receipt #%d.", cmd.Number)
	}
	if err != nil {
		s.logger.Error("looking up receipt for edit", "number", cmd.Number, "error", err)
		return "Something went wrong finding that receipt. Please try again."
	}

	if problems := cmd.Update.Validate(); len(problems) > 0 {
		var b strings.Builder
		b.WriteString("That edit isn't valid:\n")
		for field, problem := range problems {
			fmt.Fprintf(&b, "• %s %s\n", field, problem)
		}
		return b.String()
	}

	updated, err := s.deps.Receipts.UpdateReceipt(ctx, existing.ID, cmd.Update)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Sprintf("I don't have a receipt #%d.", cmd.Number)
	}
	if err != nil {
		s.logger.Error("updating receipt", "number", cmd.Number, "error", err)
		return "Something went wrong saving that edit. Please try again."
	}

	s.logger.Info("receipt edited",
		"number", updated.Number, "receipt_id", updated.ID,
		"merchant", updated.Merchant, "total", updated.Total)

	learned := s.teachStore(ctx, existing, cmd.Update)

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

func (s *Server) listStoresReply(ctx context.Context) string {
	if s.deps.Stores == nil {
		return "The store directory isn't configured on this server."
	}

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
