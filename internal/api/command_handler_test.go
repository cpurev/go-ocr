package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
)

func newCommandServer(t *testing.T, roster *relay.Roster, receipts store.ReceiptStore) *Server {
	t.Helper()

	return NewServer(
		config.Config{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps{Relay: roster, Receipts: receipts},
	)
}

func TestWhoReply(t *testing.T) {
	both := []string{"+" + alice, "+" + bob}

	tests := []struct {
		name   string
		roster *relay.Roster
		sender string
		want   string
	}{
		{
			name:   "no relay configured",
			sender: alice,
			want:   "The relay isn't set up, so it's just you and me.",
		},
		{
			name:   "empty roster",
			roster: relay.New(nil),
			sender: alice,
			want:   "The relay isn't set up, so it's just you and me.",
		},
		{
			name:   "asker is marked",
			roster: relay.New(both),
			sender: alice,
			want:   "*On the relay*\n\n+" + alice + " (you)\n+" + bob + "\n",
		},
		{
			name:   "the marker follows the asker",
			roster: relay.New(both),
			sender: bob,
			want:   "*On the relay*\n\n+" + alice + "\n+" + bob + " (you)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCommandServer(t, tt.roster, nil)

			got := srv.whoReply(context.Background(), request{Sender: tt.sender})
			if got != tt.want {
				t.Errorf("who from %s replied %q, want %q", tt.sender, got, tt.want)
			}
		})
	}
}

func TestLastReply(t *testing.T) {
	tests := []struct {
		name     string
		receipts []model.Receipt
		err      error
		want     string
	}{
		{
			name: "nothing stored yet",
			want: "I don't have any receipts yet.",
		},
		{
			name: "one receipt is singular",
			receipts: []model.Receipt{
				{Number: 48, Merchant: "ICA", Total: 154.53, Currency: "SEK", Date: "2026-08-04"},
			},
			want: "*Last receipt*\n\n#48 ICA, 154.53 SEK, 2026-08-04\n",
		},
		{
			name: "several keep the order the store gave",
			receipts: []model.Receipt{
				{Number: 48, Merchant: "ICA", Total: 154.53, Currency: "SEK", Date: "2026-08-04"},
				{Number: 47, Merchant: "Willys", Total: 89, Currency: "SEK", Date: "2026-08-01"},
			},
			want: "*Last 2 receipts*\n\n" +
				"#48 ICA, 154.53 SEK, 2026-08-04\n" +
				"#47 Willys, 89.00 SEK, 2026-08-01\n",
		},
		{
			name: "a receipt OCR could not read",
			receipts: []model.Receipt{
				{Number: 3, Total: 12.5, Currency: "SEK"},
			},
			want: "*Last receipt*\n\n#3 (no merchant), 12.50 SEK\n",
		},
		{
			name: "store outage",
			err:  errors.New("mongo is unreachable"),
			want: "Something went wrong reading your receipts. Please try again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipts := &fakeReceipts{
				listRecentReceipts: func(_ context.Context, limit int) ([]model.Receipt, error) {
					if limit != defaultRecent {
						t.Errorf("last asked the store for %d receipts, want the parsed limit %d",
							limit, defaultRecent)
					}
					return tt.receipts, tt.err
				},
			}
			srv := newCommandServer(t, nil, receipts)

			got := srv.lastReply(context.Background(),
				request{Sender: alice, Cmd: Command{Limit: defaultRecent}})
			if got != tt.want {
				t.Errorf("last replied %q, want %q", got, tt.want)
			}
		})
	}
}
