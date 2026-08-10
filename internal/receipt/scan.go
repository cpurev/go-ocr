package receipt

import (
	"context"
	"fmt"

	"github.com/cpurev/go-ocr/internal/model"
)

type TextExtractor interface {
	Text(ctx context.Context, image []byte) (string, error)
}

type Scanner struct {
	ocr    TextExtractor
	parser *Parser
}

func NewScanner(extractor TextExtractor, parser *Parser) *Scanner {
	return &Scanner{ocr: extractor, parser: parser}
}

func (s *Scanner) Scan(ctx context.Context, image []byte) (model.ReceiptFields, error) {
	if s == nil || s.ocr == nil {
		return model.ReceiptFields{}, ErrNotConfigured
	}

	text, err := s.ocr.Text(ctx, image)
	if err != nil {
		return model.ReceiptFields{}, fmt.Errorf("reading image: %w", err)
	}

	return s.parser.Parse(text), nil
}
