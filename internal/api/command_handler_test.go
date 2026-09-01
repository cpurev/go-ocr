package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

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

func TestEditWithNoNumberTargetsTheNewestByInsertionOrder(t *testing.T) {
	newest := model.Receipt{ID: "a1", Number: 48, Merchant: "ICA",
		Total: 154.53, Currency: "SEK", Date: "2026-01-02"}
	older := model.Receipt{ID: "b2", Number: 47, Merchant: "Willys",
		Total: 89, Currency: "SEK", Date: "2026-08-04"}

	var askedLimit, askedNumber int
	receipts := &fakeReceipts{
		listRecentReceipts: func(_ context.Context, limit int) ([]model.Receipt, error) {
			askedLimit = limit
			return []model.Receipt{newest, older}, nil
		},
		getReceiptByNumber: func(_ context.Context, number int) (model.Receipt, error) {
			askedNumber = number
			return newest, nil
		},
		updateReceipt: func(_ context.Context, _ string, update model.ReceiptUpdate) (model.Receipt, error) {
			edited := newest
			edited.Merchant = *update.Merchant
			return edited, nil
		},
	}
	srv := newCommandServer(t, nil, receipts)

	body := srv.editReply(context.Background(), request{Sender: alice,
		Cmd: Command{Update: model.ReceiptUpdate{Merchant: ptr("Coop")}}})

	if askedLimit != 1 {
		t.Errorf("edit asked the store for %d recent receipts, want 1", askedLimit)
	}
	if askedNumber != newest.Number {
		t.Errorf("edit with no number reached receipt #%d, want #%d: the newest receipt is the "+
			"last one inserted, not the one with the newest printed date", askedNumber, newest.Number)
	}
	if want := "*Receipt #48 updated*"; !strings.Contains(body, want) {
		t.Errorf("edit replied %q, want it to contain %q", body, want)
	}
}

func TestEditWithNoNumberWhenThereIsNothingToTarget(t *testing.T) {
	tests := []struct {
		name   string
		recent []model.Receipt
		err    error
		want   string
	}{
		{
			name: "nothing stored yet",
			want: "I don't have any receipts yet.",
		},
		{
			name: "store outage",
			err:  errors.New("mongo is unreachable"),
			want: "Something went wrong finding that receipt. Please try again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipts := &fakeReceipts{
				listRecentReceipts: func(_ context.Context, _ int) ([]model.Receipt, error) {
					return tt.recent, tt.err
				},
				getReceiptByNumber: func(_ context.Context, number int) (model.Receipt, error) {
					t.Fatalf("edit looked up receipt #%d, want no lookup at all", number)
					return model.Receipt{}, nil
				},
			}
			srv := newCommandServer(t, nil, receipts)

			got := srv.editReply(context.Background(), request{Sender: alice,
				Cmd: Command{Update: model.ReceiptUpdate{Merchant: ptr("Coop")}}})
			if got != tt.want {
				t.Errorf("edit replied %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeleteReply(t *testing.T) {
	gone := model.Receipt{ID: "a1", Number: 7, Merchant: "ICA",
		Total: 154.53, Tax: 30.91, Currency: "SEK", Date: "2026-08-04"}

	var deletedID string
	receipts := &fakeReceipts{
		getReceiptByNumber: func(_ context.Context, number int) (model.Receipt, error) {
			if number != gone.Number {
				return model.Receipt{}, store.ErrNotFound
			}
			return gone, nil
		},
		deleteReceipt: func(_ context.Context, id string) error {
			deletedID = id
			return nil
		},
	}
	srv := newCommandServer(t, nil, receipts)

	got := srv.deleteReply(context.Background(), request{Sender: alice, Cmd: Command{Number: 7}})

	want := "*Receipt #7 deleted*\n\n" +
		"Merchant: ICA\nDate: 2026-08-04\nTotal: 154.53 SEK\nTax: 30.91 SEK\n"
	if got != want {
		t.Errorf("delete replied %q, want %q", got, want)
	}
	if deletedID != gone.ID {
		t.Errorf("delete removed receipt %q, want the one it echoed, %q", deletedID, gone.ID)
	}
}

func TestDeleteReplyWhenThereIsNoSuchReceipt(t *testing.T) {
	receipts := &fakeReceipts{
		getReceiptByNumber: func(context.Context, int) (model.Receipt, error) {
			return model.Receipt{}, store.ErrNotFound
		},
		deleteReceipt: func(_ context.Context, id string) error {
			t.Fatalf("delete removed receipt %q, want nothing removed", id)
			return nil
		},
	}
	srv := newCommandServer(t, nil, receipts)

	got := srv.deleteReply(context.Background(), request{Sender: alice, Cmd: Command{Number: 7}})
	if want := "I don't have a receipt #7."; got != want {
		t.Errorf("delete replied %q, want %q", got, want)
	}
}

func TestTotalReply(t *testing.T) {
	september := Period{
		From:  time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		To:    time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC),
		Label: "September 2026",
	}

	tests := []struct {
		name   string
		scope  Scope
		totals []store.CurrencyTotal
		err    error
		want   string
	}{
		{
			name:   "one receipt is singular",
			totals: []store.CurrencyTotal{{Currency: "SEK", Total: 154.53, Count: 1}},
			want:   "*Total for September 2026*\n\n154.53 SEK (1 receipt)\n",
		},
		{
			name: "currencies are listed apart and only the undated one explains itself",
			totals: []store.CurrencyTotal{
				{Currency: "SEK", Total: 1240.50, Count: 12, Undated: 2},
				{Currency: "EUR", Total: 89, Count: 1},
			},
			want: "*Total for September 2026*\n\n" +
				"1240.50 SEK (12 receipts, 2 dated by when I got them)\n" +
				"89.00 EUR (1 receipt)\n",
		},
		{
			name:   "everyone says so in the header",
			scope:  scopeEveryone,
			totals: []store.CurrencyTotal{{Currency: "SEK", Total: 89, Count: 2}},
			want:   "*Total for September 2026, everyone*\n\n89.00 SEK (2 receipts)\n",
		},
		{
			name: "nothing in that month",
			want: "I have no receipts for September 2026.",
		},
		{
			name: "more receipts than one total can hold",
			err:  store.ErrTooManyReceipts,
			want: "That's more receipts than I can add up at once. Try a single month.",
		},
		{
			name: "store outage",
			err:  errors.New("mongo is unreachable"),
			want: "Something went wrong adding up your receipts. Please try again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked store.TotalQuery
			receipts := &fakeReceipts{
				sumReceipts: func(_ context.Context, q store.TotalQuery) ([]store.CurrencyTotal, error) {
					asked = q
					return tt.totals, tt.err
				},
			}
			srv := newCommandServer(t, nil, receipts)

			got := srv.totalReply(context.Background(),
				request{Sender: alice, Cmd: Command{Period: september, Scope: tt.scope}})
			if got != tt.want {
				t.Errorf("total replied %q, want %q", got, tt.want)
			}

			wantUser := alice
			if tt.scope == scopeEveryone {
				wantUser = ""
			}
			if asked.UserID != wantUser {
				t.Errorf("total asked the store for user %q, want %q", asked.UserID, wantUser)
			}
			if !asked.From.Equal(september.From) || !asked.To.Equal(september.To) {
				t.Errorf("total asked the store for [%s, %s), want [%s, %s)",
					asked.From, asked.To, september.From, september.To)
			}
		})
	}
}
