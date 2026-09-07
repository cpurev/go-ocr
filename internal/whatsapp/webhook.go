package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const SignatureHeader = "X-Hub-Signature-256"

func VerifySignature(appSecret string, body []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(header), []byte(want))
}

type Notification struct {
	Object string  `json:"object"`
	Entry  []Entry `json:"entry"`
}

type Entry struct {
	ID      string   `json:"id"`
	Changes []Change `json:"changes"`
}

type Change struct {
	Field string      `json:"field"`
	Value ChangeValue `json:"value"`
}

type ChangeValue struct {
	MessagingProduct string    `json:"messaging_product"`
	Messages         []Message `json:"messages"`
	Statuses         []Status  `json:"statuses"`
}

type Message struct {
	From      string        `json:"from"`
	ID        string        `json:"id"`
	Timestamp string        `json:"timestamp"`
	Type      string        `json:"type"`
	Image     *MediaContent `json:"image"`
	Text      *TextContent  `json:"text"`
}

type MediaContent struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	Caption  string `json:"caption"`
}

type TextContent struct {
	Body string `json:"body"`
}

type InboundImage struct {
	MediaID   string
	From      string
	MessageID string
	Caption   string
}

type InboundText struct {
	From      string
	MessageID string
	Body      string
}

func (n Notification) Images() []InboundImage {
	var images []InboundImage
	for _, entry := range n.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				if msg.Type != "image" || msg.Image == nil || msg.Image.ID == "" {
					continue
				}
				images = append(images, InboundImage{
					MediaID:   msg.Image.ID,
					From:      msg.From,
					MessageID: msg.ID,
					Caption:   msg.Image.Caption,
				})
			}
		}
	}
	return images
}

func (n Notification) Texts() []InboundText {
	var texts []InboundText
	for _, entry := range n.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				if msg.Type != "text" || msg.Text == nil {
					continue
				}
				texts = append(texts, InboundText{
					From:      msg.From,
					MessageID: msg.ID,
					Body:      msg.Text.Body,
				})
			}
		}
	}
	return texts
}

type Status struct {
	ID          string        `json:"id"`
	Status      string        `json:"status"`
	RecipientID string        `json:"recipient_id"`
	Errors      []StatusError `json:"errors"`
}

type StatusError struct {
	Code    int    `json:"code"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

// DeliveryFailure is a message Meta accepted and then could not deliver. The
// send call returns 200 long before this arrives, so a failed status is the
// only evidence that a recipient never got the text.
type DeliveryFailure struct {
	MessageID string
	Recipient string
	Code      int
	Reason    string
}

// OutsideWindow reports the recipient never opened a 24-hour service window.
// No retry fixes it: that recipient has to message the business number first,
// or the text has to go out as an approved template.
func (f DeliveryFailure) OutsideWindow() bool {
	return f.Code == errCodeReEngagement
}

func (n Notification) Failures() []DeliveryFailure {
	var failures []DeliveryFailure
	for _, entry := range n.Entry {
		for _, change := range entry.Changes {
			for _, st := range change.Value.Statuses {
				if st.Status != "failed" {
					continue
				}
				failures = append(failures, DeliveryFailure{
					MessageID: st.ID,
					Recipient: st.RecipientID,
					Code:      firstErrorCode(st.Errors),
					Reason:    firstErrorReason(st.Errors),
				})
			}
		}
	}
	return failures
}

func firstErrorCode(errs []StatusError) int {
	if len(errs) == 0 {
		return 0
	}
	return errs[0].Code
}

func firstErrorReason(errs []StatusError) string {
	if len(errs) == 0 {
		return ""
	}
	if errs[0].Title != "" {
		return errs[0].Title
	}
	return errs[0].Message
}
