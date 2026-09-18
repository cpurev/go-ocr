package whatsapp

import (
	"encoding/json"
	"testing"
	"time"
)

// Meta accepts a send to a recipient who never messaged the business number,
// returns 200, and only reports the failure later on this callback. Dropping it
// is what made an undelivered relay text look like a delivered one.
const failedStatusPayload = `{"object":"whatsapp_business_account","entry":[{"id":"E","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","statuses":[{"id":"wamid.FAILED","status":"failed","timestamp":"1","recipient_id":"393384068024","errors":[{"code":131047,"title":"Re-engagement message","message":"More than 24 hours have passed since the recipient last replied"}]}]}}]}]}`

func TestFailedDeliveryStatusSurfacesTheRecipientAndCode(t *testing.T) {
	var n Notification
	if err := json.Unmarshal([]byte(failedStatusPayload), &n); err != nil {
		t.Fatalf("decoding notification: %v", err)
	}

	failures := n.Failures()
	if len(failures) != 1 {
		t.Fatalf("got %d failures, want 1: an undeliverable relay text must not be silent", len(failures))
	}

	f := failures[0]
	if f.Recipient != "393384068024" {
		t.Errorf("recipient %q, want 393384068024", f.Recipient)
	}
	if f.Code != 131047 {
		t.Errorf("code %d, want 131047", f.Code)
	}
	if !f.OutsideWindow() {
		t.Error("131047 must classify as a closed 24-hour window, the one cause a retry cannot fix")
	}
}

func TestStatusCallbackCarriesNoMessages(t *testing.T) {
	var n Notification
	if err := json.Unmarshal([]byte(failedStatusPayload), &n); err != nil {
		t.Fatalf("decoding notification: %v", err)
	}

	if got := len(n.Texts()); got != 0 {
		t.Errorf("status callback yielded %d texts, want 0", got)
	}
	if got := len(n.Images()); got != 0 {
		t.Errorf("status callback yielded %d images, want 0", got)
	}
}

func TestDeliveredStatusIsNotAFailure(t *testing.T) {
	const delivered = `{"object":"whatsapp_business_account","entry":[{"id":"E","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","statuses":[{"id":"wamid.OK","status":"delivered","timestamp":"1","recipient_id":"46735859360"}]}}]}]}`

	var n Notification
	if err := json.Unmarshal([]byte(delivered), &n); err != nil {
		t.Fatalf("decoding notification: %v", err)
	}

	if got := len(n.Failures()); got != 0 {
		t.Errorf("got %d failures for a delivered status, want 0", got)
	}
}

func TestSendersCountsEveryMessageTypeAtItsSendTime(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	n := Notification{Entry: []Entry{{Changes: []Change{{Value: ChangeValue{Messages: []Message{
		{From: "46", Timestamp: "1999999000", Type: "sticker"},
		{From: "46", Timestamp: "1999999500", Type: "text"},
		{From: "39", Timestamp: "garbage", Type: "audio"},
		{From: "47", Timestamp: "2000000999", Type: "text"},
	}}}}}}}

	got := n.Senders(now)

	want := []InboundSender{
		{From: "46", At: time.Unix(1999999500, 0)},
		{From: "39", At: now},
		{From: "47", At: now},
	}
	if len(got) != len(want) {
		t.Fatalf("Senders = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].From != want[i].From || !got[i].At.Equal(want[i].At) {
			t.Errorf("Senders[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
