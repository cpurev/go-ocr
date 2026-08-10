package receipt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cpurev/go-ocr/internal/model"
)

var ErrNotConfigured = errors.New("receipt: ingestion is not configured")

type MediaDownloader interface {
	Download(ctx context.Context, mediaID string) ([]byte, error)
}

type Repository interface {
	CreateReceipt(ctx context.Context, in model.ReceiptInput, fields model.ReceiptFields) (model.Receipt, error)
}

type StoreLookup interface {
	LookupStore(ctx context.Context, orgNr string) (model.Store, error)
}

type Ingester struct {
	media   MediaDownloader
	scanner *Scanner
	repo    Repository
	stores  StoreLookup
	logger  *slog.Logger

	storeOverridesOCR bool
}

func (i *Ingester) WithStoreLookup(stores StoreLookup, overridesOCR bool) *Ingester {
	i.stores = stores
	i.storeOverridesOCR = overridesOCR
	return i
}

func (i *Ingester) applyStoreDirectory(ctx context.Context, fields *model.ReceiptFields) (orgNr, learned string) {
	if i.stores == nil {
		return "", ""
	}

	orgNr = FindOrgNr(fields.RawText)
	if orgNr == "" {
		return "", ""
	}

	store, err := i.stores.LookupStore(ctx, orgNr)
	if err != nil {
		return orgNr, ""
	}

	if fields.Merchant == "" || i.storeOverridesOCR {
		fields.Merchant = store.Merchant
		return orgNr, store.Merchant
	}

	return orgNr, ""
}

func NewIngester(media MediaDownloader, scanner *Scanner, repo Repository, logger *slog.Logger) *Ingester {
	return &Ingester{media: media, scanner: scanner, repo: repo, logger: logger}
}

func (i *Ingester) Ingest(ctx context.Context, in model.ReceiptInput) (model.Receipt, error) {
	if i.media == nil || i.scanner == nil || i.repo == nil {
		return model.Receipt{}, ErrNotConfigured
	}

	image, err := i.media.Download(ctx, in.WhatsAppMediaID)
	if err != nil {
		return model.Receipt{}, fmt.Errorf("downloading media %s: %w", in.WhatsAppMediaID, err)
	}

	fields, err := i.scanner.Scan(ctx, image)
	if err != nil {
		return model.Receipt{}, fmt.Errorf("scanning media %s: %w", in.WhatsAppMediaID, err)
	}

	orgNr, learned := i.applyStoreDirectory(ctx, &fields)

	receipt, err := i.repo.CreateReceipt(ctx, in, fields)
	if err != nil {
		return model.Receipt{}, fmt.Errorf("storing receipt: %w", err)
	}

	if i.logger != nil {
		i.logger.InfoContext(ctx, "receipt ingested",
			"receipt_id", receipt.ID,
			"number", receipt.Number,
			"org_nr", orgNr,
			"merchant_from_directory", learned != "",
			"media_id", receipt.WhatsAppMediaID,
			"merchant", receipt.Merchant,
			"date", receipt.Date,
			"total", receipt.Total,
			"currency", receipt.Currency,
			"line_items", len(receipt.LineItems),
			"text_bytes", len(fields.RawText),
		)
	}

	return receipt, nil
}
