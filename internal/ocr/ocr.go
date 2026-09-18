package ocr

import (
	"context"
	"errors"
)

var ErrEngineUnavailable = errors.New("ocr: engine unavailable")

var ErrUnreadable = errors.New("ocr: no text found in image")

// ErrLanguageMissing means tesseract runs but lacks a configured language, so
// scans work at reduced accuracy rather than failing.
var ErrLanguageMissing = errors.New("ocr: language data missing")

type Engine interface {
	Text(ctx context.Context, image []byte) (string, error)
}
