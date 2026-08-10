package ocr

import (
	"context"
	"errors"
)

var ErrEngineUnavailable = errors.New("ocr: engine unavailable")

var ErrUnreadable = errors.New("ocr: no text found in image")

type Engine interface {
	Text(ctx context.Context, image []byte) (string, error)
}
