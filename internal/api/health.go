package api

import (
	"context"
	"net/http"
	"time"

	"github.com/cpurev/go-ocr/internal/httpx"
	"github.com/cpurev/go-ocr/internal/store"
)

type healthResponse struct {
	Status string `json:"status"`
	Env    string `json:"env"`
	Uptime string `json:"uptime"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	httpx.OK(w, http.StatusOK, healthResponse{
		Status: "ok",
		Env:    s.cfg.Env,
		Uptime: time.Since(s.startedAt).Round(time.Second).String(),
	}, nil)
}

type pinger interface {
	Ping(ctx context.Context) error
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.checkDependencies(r.Context()); err != nil {
		s.logger.WarnContext(r.Context(), "readiness check failed", "error", err)
		httpx.Error(w, r, http.StatusServiceUnavailable, "service is not ready", nil)
		return
	}

	httpx.OK(w, http.StatusOK, healthResponse{
		Status: "ready",
		Env:    s.cfg.Env,
		Uptime: time.Since(s.startedAt).Round(time.Second).String(),
	}, nil)
}

func (s *Server) checkDependencies(ctx context.Context) error {
	if s.deps.Receipts != nil {
		if p, ok := s.deps.Receipts.(pinger); ok {
			return p.Ping(ctx)
		}
		if _, _, err := s.deps.Receipts.ListReceipts(ctx, store.ReceiptFilter{Limit: 1}); err != nil {
			return err
		}
	}

	return nil
}
