package store

import (
	"context"
	"errors"
	"time"
)

var ErrClaimed = errors.New("store: message already claimed")

// ClaimTTL is how long a handled message id is remembered. It only has to
// outlive Meta's webhook retry schedule.
const ClaimTTL = 48 * time.Hour

// defaultStaleAfter is the lease length used when the caller does not set one.
const defaultStaleAfter = 3 * time.Minute

// MessageClaims records which inbound WhatsApp message ids this service has
// handled, so a redelivered webhook does no work and sends no reply.
type MessageClaims interface {
	// Claim reserves messageID. ErrClaimed means an earlier delivery already
	// replied, or is still working inside its lease. A lease whose owner died
	// is taken over and Claim returns nil, so a killed instance cannot eat the
	// message.
	Claim(ctx context.Context, messageID string) error

	// Finish marks messageID replied, so every later retry is refused until
	// the TTL prunes it. Call it only after at least one send succeeded.
	Finish(ctx context.Context, messageID string) error

	// Release hands a claim back so a retry may redo the work. Releasing a
	// claim nobody holds is not an error.
	Release(ctx context.Context, messageID string) error
}

const (
	statusWorking = "working"
	statusReplied = "replied"
)
