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
	Sender    string // normalized digits
	MessageID string
	Cmd       Command
}

func (s *Server) replyToText(ctx context.Context, txt whatsapp.InboundText) Reply {
	sender := relay.Normalize(txt.From)

	v, cmd, ok := parseCommand(txt.Body, time.Now().In(s.cfg.Location()))
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

	ans := v.Run(s, ctx, request{Sender: sender, MessageID: txt.MessageID, Cmd: cmd})
	if ans.private {
		return replyToSender(sender, ans.body)
	}
	return Reply{Sender: sender, Audience: v.Audience, Body: ans.body}
}

// addReply logs a receipt with no photo: the message id stands in for the
// media id, since it is the one thing about a text that is unique the way a
// WhatsApp media download is, and it keeps a Meta redelivery from double-adding.
func (s *Server) addReply(ctx context.Context, req request) answer {
	fields := model.ReceiptFields{
		Merchant: req.Cmd.Merchant,
		Total:    req.Cmd.Total,
		Date:     req.Cmd.Date,
	}

	created, err := s.deps.Receipts.CreateReceipt(ctx, model.ReceiptInput{
		WhatsAppMediaID: req.MessageID,
		UserID:          req.Sender,
	}, fields)

	switch {
	case errors.Is(err, store.ErrDuplicate):
		s.logger.Info("webhook text add already ingested", "message_id", req.MessageID)
		return privately("I already logged that one.")
	case err != nil:
		s.logger.Error("adding receipt from text", "message_id", req.MessageID, "error", err)
		return privately("Something went wrong saving that. Please try again.")
	}

	s.logger.Info("receipt added by text",
		"receipt_id", created.ID, "merchant", created.Merchant, "total", created.Total)

	return said(formatReceiptReply(created))
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

func (s *Server) helpReply(ctx context.Context, req request) answer { return said(helpText) }

func (s *Server) whoReply(ctx context.Context, req request) answer {
	members := s.deps.Relay.Members()
	if len(members) == 0 {
		return said("The relay isn't set up, so it's just you and me.")
	}

	var b strings.Builder
	b.WriteString("*On the relay*\n\n")
	for _, number := range members {
		b.WriteString("+" + number)
		if number == req.Sender {
			b.WriteString(" (you)")
		}
		b.WriteString("\n")
	}

	return said(b.String())
}

func (s *Server) lastReply(ctx context.Context, req request) answer {
	receipts, err := s.deps.Receipts.ListRecentReceipts(ctx, req.Cmd.Limit)
	if err != nil {
		s.logger.Error("listing recent receipts", "limit", req.Cmd.Limit, "error", err)
		return said("Something went wrong reading your receipts. Please try again.")
	}
	if len(receipts) == 0 {
		return said("I don't have any receipts yet.")
	}

	var b strings.Builder
	if len(receipts) == 1 {
		b.WriteString("*Last receipt*\n\n")
	} else {
		fmt.Fprintf(&b, "*Last %d receipts*\n\n", len(receipts))
	}
	for _, r := range receipts {
		b.WriteString(formatReceiptLine(r))
		b.WriteString("\n")
	}

	return said(b.String())
}

func formatReceiptLine(r model.Receipt) string {
	merchant := r.Merchant
	if merchant == "" {
		merchant = "(no merchant)"
	}

	line := fmt.Sprintf("#%d %s, %.2f %s", r.Number, merchant, r.Total, model.Currency)
	if r.Date == "" {
		return line
	}

	return line + ", " + r.Date
}

func writeReceiptFields(b *strings.Builder, r model.Receipt) {
	if r.Merchant != "" {
		fmt.Fprintf(b, "Merchant: %s\n", r.Merchant)
	}
	if r.Date != "" {
		fmt.Fprintf(b, "Date: %s\n", r.Date)
	}
	fmt.Fprintf(b, "Total: %.2f %s\n", r.Total, model.Currency)
	if r.Tax > 0 {
		fmt.Fprintf(b, "Tax: %.2f %s\n", r.Tax, model.Currency)
	}
}

func (s *Server) editReply(ctx context.Context, req request) answer {
	number := req.Cmd.Number
	if number == 0 {
		// Newest overall rather than newest from the asking phone: the relay
		// points both phones at one shared stream, and `edit 7` already reaches
		// across it because GetReceiptByNumber has no user filter.
		recent, err := s.deps.Receipts.ListRecentReceipts(ctx, 1)
		if err != nil {
			s.logger.Error("finding the newest receipt to edit", "error", err)
			return privately("Something went wrong finding that receipt. Please try again.")
		}
		if len(recent) == 0 {
			return privately("I don't have any receipts yet.")
		}
		number = recent[0].Number
	}

	existing, err := s.deps.Receipts.GetReceiptByNumber(ctx, number)
	if errors.Is(err, store.ErrNotFound) {
		return privately(fmt.Sprintf("I don't have a receipt #%d.", number))
	}
	if err != nil {
		s.logger.Error("looking up receipt for edit", "number", number, "error", err)
		return privately("Something went wrong finding that receipt. Please try again.")
	}

	if problems := req.Cmd.Update.Validate(); len(problems) > 0 {
		var b strings.Builder
		b.WriteString("That edit isn't valid:\n")
		for field, problem := range problems {
			fmt.Fprintf(&b, "• %s %s\n", field, problem)
		}
		return privately(b.String())
	}

	updated, err := s.deps.Receipts.UpdateReceipt(ctx, existing.ID, req.Cmd.Update)
	if errors.Is(err, store.ErrNotFound) {
		return privately(fmt.Sprintf("I don't have a receipt #%d.", number))
	}
	if err != nil {
		s.logger.Error("updating receipt", "number", number, "error", err)
		return privately("Something went wrong saving that edit. Please try again.")
	}

	s.logger.Info("receipt edited",
		"number", updated.Number, "receipt_id", updated.ID,
		"merchant", updated.Merchant, "total", updated.Total)

	learned := s.teachStore(ctx, existing, req.Cmd.Update)

	return said(formatEditReply(updated, learned))
}

func (s *Server) totalReply(ctx context.Context, req request) answer {
	q := store.TotalQuery{From: req.Cmd.Period.From, To: req.Cmd.Period.To, Latest: defaultRecent}
	if req.Cmd.Scope == scopeSender {
		q.UserID = req.Sender
	}

	total, err := s.deps.Receipts.SumReceipts(ctx, q)
	if errors.Is(err, store.ErrTooManyReceipts) {
		return said("That's more receipts than I can add up at once. Try a single month.")
	}
	if err != nil {
		s.logger.Error("totalling receipts", "period", req.Cmd.Period.Label, "error", err)
		return said("Something went wrong adding up your receipts. Please try again.")
	}
	if total.Count == 0 {
		return said(fmt.Sprintf("I have no receipts for %s.", req.Cmd.Period.Label))
	}

	if req.Cmd.Scope == scopeEveryone {
		return said(s.formatEveryoneTotal(req, total))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*Total for %s*\n\n", req.Cmd.Period.Label)
	fmt.Fprintf(&b, "Total: %s\n\nLatest:\n",
		formatShare(total.Total, total.Count, total.Undated))

	for _, r := range total.Latest {
		b.WriteString(formatReceiptLine(r))
		b.WriteString("\n")
	}

	return said(b.String())
}

// formatEveryoneTotal answers "total all" with one line per phone rather than
// one combined figure, since two people sharing a relay want to know who spent
// what. The latest lines say whose each receipt is, masked to the first digits
// because the roster is short enough for that to identify it.
func (s *Server) formatEveryoneTotal(req request, total store.ReceiptTotal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*Total for %s, everyone*\n\n", req.Cmd.Period.Label)

	for _, share := range s.sharesInRosterOrder(total.ByUser) {
		b.WriteString(formatPhone(share.UserID))
		if share.UserID == req.Sender {
			b.WriteString(" (you)")
		}
		fmt.Fprintf(&b, ": %s\n", formatShare(share.Total, share.Count, share.Undated))
	}

	b.WriteString("\nLatest:\n")
	for _, r := range total.Latest {
		b.WriteString(maskPhone(r.UserID) + " " + formatReceiptLine(r))
		b.WriteString("\n")
	}

	return b.String()
}

// sharesInRosterOrder lists every roster member, including one with nothing
// this period, in the order the roster names them, then anyone else who has
// receipts (older records from before the relay existed).
func (s *Server) sharesInRosterOrder(byUser []store.UserTotal) []store.UserTotal {
	found := make(map[string]store.UserTotal, len(byUser))
	for _, u := range byUser {
		found[u.UserID] = u
	}

	var out []store.UserTotal
	for _, member := range s.deps.Relay.Members() {
		share, ok := found[member]
		if !ok {
			share = store.UserTotal{UserID: member}
		}
		out = append(out, share)
		delete(found, member)
	}
	for _, u := range byUser {
		if _, left := found[u.UserID]; left {
			out = append(out, u)
		}
	}
	return out
}

func formatShare(sum float64, count, undated int) string {
	noun := "receipts"
	if count == 1 {
		noun = "receipt"
	}
	line := fmt.Sprintf("%.2f %s (%d %s", sum, model.Currency, count, noun)
	if undated > 0 {
		line += fmt.Sprintf(", %d dated by when I got them", undated)
	}
	return line + ")"
}

func formatPhone(userID string) string {
	if userID == "" {
		return "Unknown sender"
	}
	return "+" + userID
}

// maskPhone keeps the first three digits: enough to tell the relay's phones
// apart, without printing a whole number beside every receipt.
func maskPhone(userID string) string {
	if userID == "" {
		return "?"
	}
	if len(userID) <= 3 {
		return "+" + userID
	}
	return "+" + userID[:3] + "..."
}

func (s *Server) deleteReply(ctx context.Context, req request) answer {
	number := req.Cmd.Number

	existing, err := s.deps.Receipts.GetReceiptByNumber(ctx, number)
	if errors.Is(err, store.ErrNotFound) {
		return privately(fmt.Sprintf("I don't have a receipt #%d.", number))
	}
	if err != nil {
		s.logger.Error("looking up receipt to delete", "number", number, "error", err)
		return privately("Something went wrong finding that receipt. Please try again.")
	}

	err = s.deps.Receipts.DeleteReceipt(ctx, existing.ID)
	if errors.Is(err, store.ErrNotFound) {
		return privately(fmt.Sprintf("I don't have a receipt #%d.", number))
	}
	if err != nil {
		s.logger.Error("deleting receipt",
			"number", number, "receipt_id", existing.ID, "error", err)
		return privately("Something went wrong deleting that receipt. Please try again.")
	}

	s.logger.Info("receipt deleted",
		"number", existing.Number, "receipt_id", existing.ID,
		"merchant", existing.Merchant, "total", existing.Total)

	var b strings.Builder
	fmt.Fprintf(&b, "*Receipt #%d deleted*\n\n", existing.Number)
	writeReceiptFields(&b, existing)

	return said(b.String())
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

func (s *Server) storesReply(ctx context.Context, req request) answer {
	stores, err := s.deps.Stores.ListStores(ctx)
	if err != nil {
		s.logger.Error("listing stores", "error", err)
		return said("Something went wrong reading the store list.")
	}
	if len(stores) == 0 {
		return said("I haven't learned any shops yet.\n\n" +
			"Correct one with `edit 7 merchant: ICA` and I'll remember it.")
	}

	var b strings.Builder
	b.WriteString("*Shops I know*\n\n")
	for _, st := range stores {
		fmt.Fprintf(&b, "%s (%s)\n", st.Merchant, st.OrgNr)
	}
	return said(b.String())
}

func formatEditReply(r model.Receipt, learned string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "*Receipt #%d updated*\n\n", r.Number)
	writeReceiptFields(&b, r)

	if learned != "" {
		fmt.Fprintf(&b, "\nI'll call this shop %s from now on.", learned)
	}

	return b.String()
}
