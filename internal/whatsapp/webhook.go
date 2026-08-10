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
}

type Message struct {
	From      string        `json:"from"`
	ID        string        `json:"id"`
	Timestamp string        `json:"timestamp"`
	Type      string        `json:"type"`
	Image     *MediaContent `json:"image"`
	Text      *TextContent  `json:"text"`

	GroupID string `json:"group_id"`
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
	GroupID   string
	MessageID string
	Caption   string
}

type InboundText struct {
	From      string
	GroupID   string
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
					GroupID:   msg.GroupID,
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
					GroupID:   msg.GroupID,
					MessageID: msg.ID,
					Body:      msg.Text.Body,
				})
			}
		}
	}
	return texts
}
