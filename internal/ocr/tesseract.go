package ocr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Tesseract struct {
	binary  string
	lang    string
	psm     int
	timeout time.Duration
}

var _ Engine = (*Tesseract)(nil)

const defaultPSM = 6

func NewTesseract(binary, lang string, timeout time.Duration) *Tesseract {
	if binary == "" {
		binary = "tesseract"
	}
	if lang == "" {
		lang = "eng"
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Tesseract{binary: binary, lang: lang, psm: defaultPSM, timeout: timeout}
}

func (t *Tesseract) Available() error {
	if _, err := exec.LookPath(t.binary); err != nil {
		return fmt.Errorf("%w: %q not found on PATH (brew install tesseract): %w",
			ErrEngineUnavailable, t.binary, err)
	}
	return nil
}

func (t *Tesseract) Text(ctx context.Context, image []byte) (string, error) {
	if len(image) == 0 {
		return "", fmt.Errorf("%w: empty image", ErrUnreadable)
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, t.binary,
		"stdin", "stdout",
		"-l", t.lang,
		"--psm", strconv.Itoa(t.psm),
	)
	cmd.Stdin = bytes.NewReader(image)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("%w: running %q: %w", ErrEngineUnavailable, t.binary, err)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("ocr: tesseract timed out after %s", t.timeout)
		}

		return "", fmt.Errorf("%w: tesseract: %w: %s",
			ErrUnreadable, err, firstLine(stderr.String()))
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "", ErrUnreadable
	}

	return text, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		s = s[:max]
	}
	return s
}
