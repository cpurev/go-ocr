package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxTextBody = 4096

const (
	recipientIndividual = "individual"
	recipientGroup      = "group"
)

// Meta's code for a recipient who has not messaged the business in 24 hours.
const errCodeReEngagement = 131047

var ErrOutsideWindow = errors.New("whatsapp: recipient's 24-hour service window is closed")

type graphError struct {
	Error struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// graphErrorCode returns Meta's error code, or 0 if the body is not an error.
func graphErrorCode(body []byte) int {
	var ge graphError
	if err := json.Unmarshal(body, &ge); err != nil {
		return 0
	}
	return ge.Error.Code
}

type Sender struct {
	http          *http.Client
	baseURL       string
	token         string
	phoneNumberID string
}

func NewSender(baseURL, token, phoneNumberID string, timeout time.Duration) *Sender {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &Sender{
		http:          &http.Client{Timeout: timeout},
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		token:         token,
		phoneNumberID: phoneNumberID,
	}
}

type textMessage struct {
	MessagingProduct string      `json:"messaging_product"`
	RecipientType    string      `json:"recipient_type"`
	To               string      `json:"to"`
	Type             string      `json:"type"`
	Text             textPayload `json:"text"`
}

type textPayload struct {
	PreviewURL bool   `json:"preview_url"`
	Body       string `json:"body"`
}

// SendText sends a 1:1 message to a phone number.
func (s *Sender) SendText(ctx context.Context, to, body string) error {
	return s.sendText(ctx, recipientIndividual, to, body)
}

// SendGroupText sends a message to a group, using the group_id from the webhook.
func (s *Sender) SendGroupText(ctx context.Context, groupID, body string) error {
	return s.sendText(ctx, recipientGroup, groupID, body)
}

func (s *Sender) sendText(ctx context.Context, recipientType, to, body string) error {
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("whatsapp: empty recipient")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("whatsapp: empty message body")
	}
	if s.phoneNumberID == "" {
		return fmt.Errorf("whatsapp: no phone number id configured")
	}

	if len([]rune(body)) > maxTextBody {
		body = string([]rune(body)[:maxTextBody])
	}

	payload, err := json.Marshal(textMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    recipientType,
		To:               to,
		Type:             "text",
		Text:             textPayload{PreviewURL: false, Body: body},
	})
	if err != nil {
		return fmt.Errorf("whatsapp: encoding message: %w", err)
	}

	url := s.baseURL + "/" + s.phoneNumberID + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("whatsapp: building send request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp: sending message: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return fmt.Errorf("whatsapp: reading send response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode >= 300:
		if graphErrorCode(respBody) == errCodeReEngagement {
			return ErrOutsideWindow
		}
		return fmt.Errorf("whatsapp: send failed with status %d: %s",
			resp.StatusCode, firstLine(string(respBody)))
	}

	return nil
}
