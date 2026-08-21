package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/cpurev/go-ocr/internal/api"
	"github.com/cpurev/go-ocr/internal/config"
	"github.com/cpurev/go-ocr/internal/ocr"
	"github.com/cpurev/go-ocr/internal/receipt"
	"github.com/cpurev/go-ocr/internal/relay"
	"github.com/cpurev/go-ocr/internal/store"
	"github.com/cpurev/go-ocr/internal/whatsapp"
)

const timeoutBody = `{"success":false,"error":{"message":"request timed out"}}`

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)

	engine := ocr.NewTesseract(cfg.TesseractBin, cfg.TesseractLang, cfg.OCRTimeout)

	if err := engine.Available(); err != nil {
		logger.Warn("OCR engine is not available; scanning will return 503",
			"binary", cfg.TesseractBin,
			"hint", "brew install tesseract (macOS) or apt-get install tesseract-ocr",
			"error", err)
	}
	scanner := receipt.NewScanner(engine, receipt.New(cfg.ReceiptDayFirst, cfg.ReceiptCurrency))

	var (
		receiptStore store.ReceiptStore
		repo         receipt.Repository
		directory    api.StoreDirectory
		lookup       receipt.StoreLookup
	)
	if cfg.MongoURI != "" {
		client, err := connectMongo(cfg)
		if err != nil {
			return err
		}

		defer func() {
			disconnectCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			defer cancel()
			if err := client.Disconnect(disconnectCtx); err != nil {
				logger.Error("disconnecting from mongodb", "error", err)
			} else {
				logger.Info("mongodb connection closed")
			}
		}()

		logger.Info("connected to mongodb",

			"uri", cfg.MongoURISafe(),
			"database", cfg.MongoDatabase,
			"collection", cfg.MongoReceipts,
		)

		db := client.Database(cfg.MongoDatabase)

		counters := store.NewMongoCounters(db.Collection(cfg.MongoCounters))

		receipts := store.NewMongoReceipts(db.Collection(cfg.MongoReceipts), counters, logger)

		stores := store.NewMongoStores(db.Collection(cfg.MongoStores))

		indexCtx, cancelIndex := context.WithTimeout(context.Background(), cfg.MongoTimeout)
		defer cancelIndex()
		if err := receipts.EnsureIndexes(indexCtx); err != nil {
			return err
		}
		if err := stores.EnsureIndexes(indexCtx); err != nil {
			return err
		}

		receiptStore = receipts
		repo = receipts
		directory = stores
		lookup = stores
	} else {
		logger.Warn("ATLAS is not set; receipts will not be persisted and /api/v1/receipts will return 503",
			"hint", "add ATLAS=\"mongodb+srv://...\" to .env to enable storage")
	}

	var media receipt.MediaDownloader
	if cfg.WhatsAppToken != "" {
		media = whatsapp.NewClient(cfg.WhatsAppAPIBase, cfg.WhatsAppToken,
			cfg.WhatsAppTimeout, cfg.MediaMaxBytes)
	} else {
		logger.Warn("WHATSAPP_TOKEN is not set; POST /api/v1/receipts will return 503",
			"hint", "add WHATSAPP_TOKEN to .env to enable receipt ingestion")
	}

	var replier api.Replier
	switch {
	case cfg.WhatsAppToken == "":

	case cfg.WhatsAppPhoneNumberID == "":
		logger.Warn("WHATSAPP_PHONE_NUMBER_ID is not set; receipts are ingested but not acknowledged",
			"hint", "add WHATSAPP_PHONE_NUMBER_ID to .env to reply to senders on WhatsApp")
	default:
		replier = whatsapp.NewSender(cfg.WhatsAppAPIBase, cfg.WhatsAppToken,
			cfg.WhatsAppPhoneNumberID, cfg.WhatsAppTimeout)
	}

	ingester := receipt.NewIngester(media, scanner, repo, logger).
		WithStoreLookup(lookup, cfg.StoreOverridesOCR)

	roster := relay.New(cfg.RelayNumbers)
	switch {
	case roster.Active():
		logger.Info("relay enabled; replies fan out to all participants",
			"participants", roster.Size())
	case roster.Size() == 1:
		logger.Warn("WHATSAPP_RELAY_NUMBERS holds a single number; relaying is inert",
			"hint", "list every participant, comma separated, to fan messages out")
	}

	srv := api.NewServer(cfg, logger, api.Deps{
		Scanner:  scanner,
		Receipts: receiptStore,
		Ingester: ingester,
		Replier:  replier,
		Stores:   directory,
		Relay:    roster,
	})

	httpServer := &http.Server{
		Addr: cfg.Addr,

		Handler: http.TimeoutHandler(srv.Routes(), cfg.RequestTimeout, timeoutBody),

		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,

		ReadHeaderTimeout: cfg.ReadTimeout,

		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Addr, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("http server listening",
		"addr", listener.Addr().String(),
		"env", cfg.Env,
		"log_level", cfg.LogLevel.String(),
	)

	return serve(ctx, logger, httpServer, listener, cfg.ShutdownTimeout)
}

func serve(
	ctx context.Context,
	logger *slog.Logger,
	httpServer *http.Server,
	listener net.Listener,
	shutdownTimeout time.Duration,
) error {
	serverErr := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)

	case <-ctx.Done():
		logger.Info("shutdown signal received, draining connections",
			"timeout", shutdownTimeout.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	logger.Info("server stopped cleanly")
	return nil
}

func connectMongo(cfg config.Config) (*mongo.Client, error) {
	opts := options.Client().
		ApplyURI(cfg.MongoURI).
		SetAppName("go-ocr-api").
		SetServerSelectionTimeout(cfg.MongoTimeout).
		SetConnectTimeout(cfg.MongoTimeout)

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongodb: invalid connection options: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.MongoTimeout)
	defer cancel()

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongodb: cannot reach %s: %w", cfg.MongoURISafe(), err)
	}

	return client, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}

	var handler slog.Handler
	if cfg.Env == "development" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler).With("service", "go-ocr-api")
}
