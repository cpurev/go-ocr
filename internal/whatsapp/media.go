package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrMediaNotFound = errors.New("whatsapp: media not found or expired")

	ErrUnauthorized = errors.New("whatsapp: unauthorized")

	ErrNotImage = errors.New("whatsapp: media is not an image")
)

const DefaultBaseURL = "https://graph.facebook.com/v21.0"

type Client struct {
	http     *http.Client
	baseURL  string
	token    string
	maxBytes int64
}

type Media struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	SHA256   string `json:"sha256"`
}

func NewClient(baseURL, token string, timeout time.Duration, maxBytes int64) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}

	return &Client{
		http:     &http.Client{Timeout: timeout},
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		token:    token,
		maxBytes: maxBytes,
	}
}

func (c *Client) Metadata(ctx context.Context, mediaID string) (Media, error) {
	if strings.TrimSpace(mediaID) == "" {
		return Media{}, fmt.Errorf("%w: empty media id", ErrMediaNotFound)
	}

	body, _, err := c.get(ctx, c.baseURL+"/"+mediaID)
	if err != nil {
		return Media{}, err
	}

	var media Media
	if err := json.Unmarshal(body, &media); err != nil {
		return Media{}, fmt.Errorf("whatsapp: decoding media metadata: %w", err)
	}
	if media.URL == "" {
		return Media{}, fmt.Errorf("%w: response contained no url", ErrMediaNotFound)
	}

	return media, nil
}

func (c *Client) Download(ctx context.Context, mediaID string) ([]byte, error) {
	media, err := c.Metadata(ctx, mediaID)
	if err != nil {
		return nil, err
	}

	if media.MimeType != "" && !strings.HasPrefix(media.MimeType, "image/") {
		return nil, fmt.Errorf("%w: got %s", ErrNotImage, media.MimeType)
	}
	if media.FileSize > c.maxBytes {
		return nil, fmt.Errorf("whatsapp: media is %d bytes, limit is %d", media.FileSize, c.maxBytes)
	}

	image, contentType, err := c.get(ctx, media.URL)
	if err != nil {
		return nil, err
	}

	if contentType != "" && !strings.HasPrefix(contentType, "image/") &&
		!strings.HasPrefix(contentType, "application/octet-stream") {
		return nil, fmt.Errorf("%w: served as %s", ErrNotImage, contentType)
	}
	if len(image) == 0 {
		return nil, fmt.Errorf("%w: empty body", ErrMediaNotFound)
	}

	return image, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("whatsapp: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	req.Header.Set("User-Agent", "go-ocr/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("whatsapp: request failed: %w", redactURLError(err))
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("whatsapp: reading response: %w", err)
	}
	if int64(len(body)) > c.maxBytes {
		return nil, "", fmt.Errorf("whatsapp: response exceeds %d bytes", c.maxBytes)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusBadRequest:
		return nil, "", fmt.Errorf("%w: status %d: %s",
			ErrMediaNotFound, resp.StatusCode, firstLine(string(body)))
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, "", ErrUnauthorized
	case resp.StatusCode >= 300:
		return nil, "", fmt.Errorf("whatsapp: unexpected status %d: %s",
			resp.StatusCode, firstLine(string(body)))
	}

	return body, resp.Header.Get("Content-Type"), nil
}

func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
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
