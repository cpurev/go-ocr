package api

import (
	"errors"
	"net/http"

	"github.com/cpurev/go-ocr/internal/httpx"
	"github.com/cpurev/go-ocr/internal/model"
	"github.com/cpurev/go-ocr/internal/ocr"
	"github.com/cpurev/go-ocr/internal/receipt"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

func (s *Server) handleCreateReceipt(w http.ResponseWriter, r *http.Request) {
	if s.deps.Ingester == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"receipt ingestion is not configured on this server", nil)
		return
	}

	in, err := httpx.DecodeJSON[model.ReceiptInput](w, r)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, err.Error(), nil)
		return
	}

	if problems := in.Validate(); len(problems) > 0 {
		httpx.Error(w, r, http.StatusUnprocessableEntity, "validation failed", problems)
		return
	}

	created, err := s.deps.Ingester.Ingest(r.Context(), in)
	if err != nil {
		s.handleIngestError(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/v1/receipts/"+created.ID)
	httpx.OK(w, http.StatusCreated, created, nil)
}

func (s *Server) handleListReceipts(w http.ResponseWriter, r *http.Request) {
	if s.deps.Receipts == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "receipts are not configured", nil)
		return
	}

	filter, err := receiptFilterFrom(r)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, err.Error(), nil)
		return
	}

	filter, problems := filter.Validate()
	if len(problems) > 0 {
		httpx.Error(w, r, http.StatusUnprocessableEntity, "invalid filter", problems)
		return
	}

	receipts, total, err := s.deps.Receipts.ListReceipts(r.Context(), filter)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	if receipts == nil {
		receipts = []model.Receipt{}
	}

	httpx.OK(w, http.StatusOK, receipts, &httpx.Meta{
		Total:  int(total),
		Limit:  filter.Limit,
		Offset: filter.Offset,
	})
}

func (s *Server) handleGetReceipt(w http.ResponseWriter, r *http.Request) {
	if s.deps.Receipts == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "receipts are not configured", nil)
		return
	}

	found, err := s.deps.Receipts.GetReceipt(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, r, http.StatusNotFound, "receipt not found", nil)
			return
		}
		s.serverError(w, r, err)
		return
	}

	httpx.OK(w, http.StatusOK, found, nil)
}

func receiptFilterFrom(r *http.Request) (store.ReceiptFilter, error) {
	q := httpx.Params(r)

	filter := store.ReceiptFilter{
		Merchant: q.String("merchant"),
		UserID:   q.String("user_id"),
		GroupID:  q.String("group_id"),
		Currency: q.String("currency"),
		DateFrom: q.String("date_from"),
		DateTo:   q.String("date_to"),
	}

	var err error
	if filter.MinTotal, err = q.Float("min_total"); err != nil {
		return filter, err
	}
	if filter.MaxTotal, err = q.Float("max_total"); err != nil {
		return filter, err
	}
	if filter.Limit, err = q.Int("limit", 0); err != nil {
		return filter, err
	}
	if filter.Offset, err = q.Int("offset", 0); err != nil {
		return filter, err
	}

	return filter, nil
}

func (s *Server) handleIngestError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, whatsapp.ErrMediaNotFound):
		httpx.Error(w, r, http.StatusUnprocessableEntity,
			"the WhatsApp media id is unknown or its download link has expired", nil)

	case errors.Is(err, whatsapp.ErrNotImage):
		httpx.Error(w, r, http.StatusUnprocessableEntity,
			"the WhatsApp media is not an image", nil)

	case errors.Is(err, ocr.ErrUnreadable):
		httpx.Error(w, r, http.StatusUnprocessableEntity,
			"no text could be read from the image; try a sharper, better-lit photo", nil)

	case errors.Is(err, store.ErrDuplicate):
		httpx.Error(w, r, http.StatusConflict,
			"this receipt image has already been ingested", nil)

	case errors.Is(err, whatsapp.ErrUnauthorized):
		s.logger.ErrorContext(r.Context(), "whatsapp rejected our credentials",
			"request_id", httpx.RequestIDFrom(r.Context()), "error", err)
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"cannot reach WhatsApp media service", nil)

	case errors.Is(err, ocr.ErrEngineUnavailable):
		s.logger.ErrorContext(r.Context(), "ocr engine unavailable",
			"request_id", httpx.RequestIDFrom(r.Context()), "error", err)
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"the OCR engine is unavailable", nil)

	case errors.Is(err, receipt.ErrNotConfigured):
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"receipt ingestion is not configured on this server", nil)

	default:
		s.serverError(w, r, err)
	}
}
