package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cpurev/go-ocr/internal/httpx"
	"github.com/cpurev/go-ocr/internal/model"
)

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scanner == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable,
			"OCR scanning is not configured on this server", nil)
		return
	}

	image, err := imageFromRequest(w, r, s.cfg.MediaMaxBytes)
	if err != nil {
		scanRequestError(w, r, err)
		return
	}

	fields, err := s.deps.Scanner.Scan(r.Context(), image)
	if err != nil {
		s.handleIngestError(w, r, err)
		return
	}

	httpx.OK(w, http.StatusOK, scanResponseFrom(fields.Normalize()), nil)
}

type scanResponse struct {
	Merchant  string           `json:"merchant"`
	Date      string           `json:"date"`
	Currency  string           `json:"currency"`
	Subtotal  float64          `json:"subtotal"`
	Tax       float64          `json:"tax"`
	Total     float64          `json:"total"`
	LineItems []model.LineItem `json:"line_items"`
	RawText   string           `json:"raw_text"`
}

func scanResponseFrom(f model.ReceiptFields) scanResponse {
	return scanResponse{
		Merchant:  f.Merchant,
		Date:      f.Date,
		Currency:  f.Currency,
		Subtotal:  f.Subtotal,
		Tax:       f.Tax,
		Total:     f.Total,
		LineItems: f.LineItems,
		RawText:   f.RawText,
	}
}

var (
	errNoImage          = errors.New("request contained no image data")
	errUnsupportedMedia = errors.New(
		`send the image as multipart/form-data with a file field named "image", or as a raw body with an image/* content type`)
)

func imageFromRequest(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	contentType := r.Header.Get("Content-Type")

	var (
		image []byte
		err   error
	)
	switch {
	case strings.HasPrefix(contentType, "multipart/form-data"):
		file, _, formErr := r.FormFile("image")
		if formErr != nil {
			return nil, fmt.Errorf("reading multipart form: %w", formErr)
		}
		defer file.Close()
		image, err = io.ReadAll(file)

	case strings.HasPrefix(contentType, "image/"),
		contentType == "application/octet-stream",
		contentType == "":
		image, err = io.ReadAll(r.Body)

	default:
		return nil, errUnsupportedMedia
	}

	if err != nil {
		return nil, fmt.Errorf("reading image body: %w", err)
	}
	if len(image) == 0 {
		return nil, errNoImage
	}
	return image, nil
}

func scanRequestError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		httpx.Error(w, r, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the image exceeds the %d-byte limit", tooLarge.Limit), nil)
	case errors.Is(err, errUnsupportedMedia):
		httpx.Error(w, r, http.StatusUnsupportedMediaType, err.Error(), nil)
	default:
		httpx.Error(w, r, http.StatusBadRequest, err.Error(), nil)
	}
}
