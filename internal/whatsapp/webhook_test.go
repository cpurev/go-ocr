package whatsapp

import (
	"encoding/json"
	"testing"
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
