package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/httpx"
	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/receipt"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
)

type Deps struct {
	Scanner *receipt.Scanner

	Receipts store.ReceiptStore

	Ingester *receipt.Ingester

	Replier Replier

	Stores StoreDirectory

	// Relay is optional; nil keeps every conversation 1:1.
	Relay *relay.Roster

	Claims store.MessageClaims

	// Outbox holds relays for roster members whose 24-hour window is closed.
	Outbox store.Outbox
}

type StoreDirectory interface {
	SaveStore(ctx context.Context, orgNr, merchant string) (model.Store, error)
	ListStores(ctx context.Context) ([]model.Store, error)
}

type Replier interface {
	// SendText returns the message id Meta assigned to the accepted text.
	SendText(ctx context.Context, to, body string) (string, error)
}

type Server struct {
	cfg       config.Config
	logger    *slog.Logger
	deps      Deps
	startedAt time.Time
}

func NewServer(cfg config.Config, logger *slog.Logger, deps Deps) *Server {
	// Resolved once here so no handler ever has to branch on a missing claim
	// store, which is the branch that would put the duplicate reply back.
	if deps.Claims == nil {
		deps.Claims = store.NewMemoryClaims(store.ClaimLease)
	}
	// Same for the outbox: a nil one would put back the silent relay loss.
	if deps.Outbox == nil {
		deps.Outbox = store.NewMemoryOutbox()
	}

	return &Server{
		cfg:       cfg,
		logger:    logger,
		deps:      deps,
		startedAt: time.Now(),
	}
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "unhandled server error",
		"request_id", httpx.RequestIDFrom(r.Context()),
		"method", r.Method,
		"path", r.URL.Path,
		"error", err,
	)
	httpx.Error(w, r, http.StatusInternalServerError, "internal server error", nil)
}
