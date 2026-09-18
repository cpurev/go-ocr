package store

import (
	"context"
	"time"
)

// HeldTTL is how long a relayed message waits for its recipient before it is
// dropped. It bounds a collection nobody would otherwise prune if one phone
// stopped using the bot for good.
const HeldTTL = 30 * 24 * time.Hour

// HeldMessage is a relayed text that could not go out yet because its
// recipient's 24-hour service window was closed.
type HeldMessage struct {
	To     string
	From   string
	Body   string
	HeldAt time.Time
}

// Outbox tracks when each roster member last wrote to the bot and holds relays
// for members who have not written recently. WhatsApp only delivers free-form
// text inside 24 hours of the recipient's own last message; outside it Meta
// accepts the send and then fails it with 131047, so without a hold the text
// is lost and the sender never finds out.
type Outbox interface {
	// Seen records that number wrote to the bot at at. An older time than the
	// one on record is ignored, so a late webhook retry cannot shrink the window.
	Seen(ctx context.Context, number string, at time.Time) error

	// LastSeen returns when number last wrote, or the zero time if never.
	LastSeen(ctx context.Context, number string) (time.Time, error)

	// Hold queues m and returns how many messages now wait for m.To.
	Hold(ctx context.Context, m HeldMessage) (int, error)

	// Pending reports how many messages wait for to.
	Pending(ctx context.Context, to string) (int, error)

	// Take removes and returns up to limit messages for to, oldest first. Each
	// one is removed atomically, so two instances cannot both deliver it.
	Take(ctx context.Context, to string, limit int) ([]HeldMessage, error)
}
