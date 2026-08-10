# go-ocr

A Go HTTP service that takes a receipt image, runs it through Tesseract OCR, and
returns structured data. The two fields that matter are the total and the date.
Merchant, line items, tax, and currency are best effort: the parser returns
whatever it can read and never fails over a missing field.

The core needs no database and no WhatsApp account. Both integrations live in
separate packages and boot only when configured.

| Module   | Package             | Enabled by       | Without it                        |
| -------- | ------------------- | ---------------- | --------------------------------- |
| Core OCR | `internal/ocr` + `internal/receipt` | tesseract binary | `/api/v1/scan` answers 503 |
| MongoDB  | `internal/store`    | `ATLAS`          | `/api/v1/receipts` answers 503    |
| WhatsApp | `internal/whatsapp` | `WHATSAPP_TOKEN` | `POST /api/v1/receipts` answers 503 |

Stack: the standard library (`net/http`, `log/slog`, `encoding/json`, `context`,
`os/exec`) plus the official `mongo-driver/v2`. No web framework, no ORM, no
`.env` library, no cgo.

```
                                 CORE
                     ┌──────────────────────────┐
any image ──────────►│ Tesseract ──► parser     │──► extracted data
POST /api/v1/scan    │ internal/ocr  internal/  │    (nothing stored)
                     │               receipt    │
                     └──────────────────────────┘
                        ▲                    │
   WhatsApp module      │                    ▼      MongoDB module
   Cloud API download ──┘              searchable receipts
   internal/whatsapp                   internal/store
   (POST /api/v1/receipts runs: download → scan → store)
```

---

## Quick start

```bash
brew install tesseract         # or: apt-get install tesseract-ocr
make run                       # start on :8080, no database needed for scanning
curl localhost:8080/healthz    # liveness

# scan any receipt image, get data back, store nothing
curl -s -X POST localhost:8080/api/v1/scan -F image=@receipt.jpg
# ...or as a raw body:
curl -s -X POST localhost:8080/api/v1/scan \
  -H 'Content-Type: image/jpeg' --data-binary @receipt.jpg
```

With `ATLAS` set, receipts persist and become searchable. With `WHATSAPP_TOKEN`
set, they can be ingested straight from a media id:

```bash
cp .env.example .env           # put your Atlas connection string in ATLAS
curl localhost:8080/readyz     # readiness, pings MongoDB when configured

# ingest a WhatsApp receipt photo (needs WHATSAPP_TOKEN + ATLAS)
curl -i -X POST localhost:8080/api/v1/receipts \
  -H 'Content-Type: application/json' \
  -d '{"whatsapp_media_id":"media_abc123xyz","user_id":"whatsapp_user_123456","group_id":"whatsapp_group_789"}'

# search them
curl -s 'localhost:8080/api/v1/receipts?merchant=starbucks'
curl -s 'localhost:8080/api/v1/receipts?date_from=2025-08-01&date_to=2025-08-31'
curl -s 'localhost:8080/api/v1/receipts?user_id=whatsapp_user_123456&min_total=10'

# fetch one (use an id from the responses above)
curl -s localhost:8080/api/v1/receipts/<id>
```

`make help` lists every task. `make check` runs format, vet, and build.

---

## Layout

```
cmd/api/main.go        the binary: config, Mongo connect, signals, shutdown
internal/config/       environment + .env configuration, validated at startup
internal/model/        domain types and validation rules (no HTTP, no storage)
internal/store/        persistence behind an interface (MongoDB)
internal/ocr/          OCR engine interface + Tesseract (via os/exec, no cgo)
internal/whatsapp/     Cloud API media download (the 2-step dance)
internal/receipt/      OCR text to structured fields, and the ingest pipeline
internal/httpx/        HTTP plumbing: envelope, JSON decoding, middleware
internal/api/          routes and handlers
```

Dependencies point one way: `api → store → model`. `model` imports nothing of
ours. `internal/` is special to the Go toolchain, so nothing outside this module
can import it.

### Request lifecycle

```
client
  └─ http.Server            timeouts, connection handling
     └─ TimeoutHandler      caps handler runtime → 503
        └─ RequestID        assigns/propagates X-Request-Id
           └─ Logger        one structured line per request
              └─ Recoverer  turns a panic into a 500
                 └─ ServeMux    method + path matching
                    └─ handler  decode → validate → store → respond
                       └─ store.MongoReceipts → mongo-driver → Atlas
```

---

## The OCR pipeline

The core is `internal/receipt.Scanner`: image bytes in, structured fields out.
`POST /api/v1/scan` calls it directly. The WhatsApp ingest wraps that core with
the two integrations, and `internal/receipt.Ingester` composes download, scan,
and store, each behind an interface:

1. Download (`internal/whatsapp`). The Cloud API needs two calls: resolve the
   media id to a short-lived URL, then fetch that URL. Both need the bearer
   token, and forgetting it on the second is the usual mistake.
2. OCR (`internal/ocr`). Shells out to `tesseract stdin stdout`, so the image
   never touches disk and the build stays pure Go. The alternative, `gosseract`,
   needs cgo and costs you cross-compilation.
3. Parse (`internal/receipt/parse.go`). Heuristics, not a grammar. It scans for
   recognizable shapes and never returns an error: unreadable text yields empty
   fields, and the raw text is still stored so a user can correct it. Priority
   order is the total and the date first; merchant, line items, tax, and
   currency are best-effort extras.
4. Store (`internal/store/receipt_mongo.go`).

Interfaces are declared in the consumer (`internal/receipt`), each listing only
the methods it uses.

Failures are kept distinct, because the status code tells a client what to do
about it:

| Failure                 | Status | Meaning to the client              |
| ----------------------- | ------ | ---------------------------------- |
| media id expired/unknown| 422    | your input; retrying will not help |
| media is a video        | 422    | your input                         |
| photo unreadable        | 422    | retake the photo                   |
| already ingested        | 409    | just GET it                        |
| tesseract missing       | 503    | our problem; retry later           |
| WhatsApp token rejected | 503    | our problem                        |

### Tuning the parser

Every receipt layout is different, so expect to teach it new shapes. Rules live
in `internal/receipt/parse.go`. `GET /api/v1/receipts/{id}` returns `raw_text`,
so the OCR output of any stored receipt is available to work from.

Two narrower loops when you are iterating on one layer:

```bash
tesseract photo.jpg stdout            # what does OCR alone see?
curl -s -X POST localhost:8080/api/v1/scan -F image=@photo.jpg
```

---

## The MongoDB layer

`internal/store/receipt_mongo.go` is the only file that imports the driver.
Everything above it works against the three-method `store.ReceiptStore`
interface, so the handlers never touch `mongo` at all.

`receiptDocument` and `model.Receipt` are separate structs. The database wants
`_id` as an ObjectID and camelCase keys, the API wants a string `id` and
snake_case, and `bson:` tags on a separate struct keep the schema and the API
contract free to change independently. Driver errors are translated at this
boundary too: `mongo.ErrNoDocuments` becomes `store.ErrNotFound`, so nothing
above imports `mongo` just to recognize a 404. A malformed id is a 404 rather
than a 500, since `"banana"` cannot be an ObjectID and so cannot match a
document. No round trip needed.

A unique index on `whatsappMediaId` rejects a re-ingest of the same image in the
database rather than in an if-statement. An existence check in code would race
with a concurrent request. `EnsureIndexes` runs on boot because the listing
sorts by `date`, and an unindexed sort makes MongoDB sort the whole collection
in memory; index creation is idempotent, so a fresh cluster needs no manual
setup. There is no mutex anywhere: the driver is goroutine-safe and pools
connections, so one `*mongo.Client` per process is shared.

---

## API

| Method | Path                    | Purpose                          | Needs             | Success |
| ------ | ----------------------- | -------------------------------- | ----------------- | ------- |
| GET    | `/healthz`              | liveness (no dependencies)       | none              | 200     |
| GET    | `/readyz`               | readiness (pings MongoDB if configured) | none       | 200     |
| POST   | `/api/v1/scan`          | scan an uploaded image, store nothing | tesseract    | 200     |
| POST   | `/api/v1/receipts`      | ingest a WhatsApp receipt image  | all three modules | 201     |
| GET    | `/api/v1/receipts`      | search receipts (see filters)    | MongoDB           | 200     |
| GET    | `/api/v1/receipts/{id}` | fetch one receipt                | MongoDB           | 200     |
| GET    | `/api/v1/whatsapp/webhook` | Meta's one-time verification handshake | `WHATSAPP_VERIFY_TOKEN` | 200 |
| POST   | `/api/v1/whatsapp/webhook` | Meta pushes message notifications here | `WHATSAPP_APP_SECRET` | 200 |

### Scan

`POST /api/v1/scan` takes the image in the request, either as
`multipart/form-data` with a file field named `image` or as a raw body with an
`image/*` content type, and returns the extracted fields. Nothing is stored, so
it works on a deployment with no database at all. An endpoint whose module is
missing answers 503, which makes a scan-only deployment legitimate rather than
broken.

### Receipts

`POST /api/v1/receipts` takes only the WhatsApp coordinates. Everything else is
derived from the image, so a client cannot claim a total by sending one:

```json
{ "whatsapp_media_id": "media_abc123xyz",
  "user_id": "whatsapp_user_123456",
  "group_id": "whatsapp_group_789" }
```

It downloads the image, OCRs it, parses it, and stores it. Ingesting the same
`whatsapp_media_id` twice returns 409, enforced by the unique index, so even
concurrent requests cannot create a duplicate.

`GET /api/v1/receipts` filters are all optional and combinable:

| Parameter               | Meaning                              |
| ----------------------- | ------------------------------------ |
| `merchant`              | case-insensitive substring           |
| `user_id` / `group_id`  | exact match                          |
| `currency`              | exact, e.g. `USD`                    |
| `date_from` / `date_to` | inclusive, `YYYY-MM-DD`              |
| `min_total`/`max_total` | numeric range                        |
| `limit` / `offset`      | paging (default 50, max 200)         |

Results come back newest-purchase-first. `meta.total` is the number of matches,
not the size of the page, so a client can tell 50-of-50 from 50-of-2000.

### The WhatsApp webhook

With the webhook configured, a user sends a photo to your WhatsApp number, Meta
calls the webhook, and the receipt appears in MongoDB. Nothing calls
`POST /api/v1/receipts` by hand.

Setup, in order:

1. Add `WHATSAPP_VERIFY_TOKEN` (any string you invent) and `WHATSAPP_APP_SECRET`
   (Meta dashboard → App settings → Basic) to `.env`, alongside
   `WHATSAPP_TOKEN` and `ATLAS`, then start the service.
2. Expose it publicly: `ngrok http 8080` or a Cloudflare tunnel in development,
   your deployed HTTPS URL in production.
3. In the Meta dashboard (WhatsApp → Configuration → Webhook) set the callback
   URL to `https://<your-host>/api/v1/whatsapp/webhook`, paste the same verify
   token, and save. Meta immediately fires the GET handshake, which only
   succeeds if the tokens match.
4. Subscribe to the `messages` webhook field.

The POST handler defends itself in the order the code runs. It reads the raw
body first, because the `X-Hub-Signature-256` HMAC covers the exact bytes.
It checks the signature second, computed with the app secret and compared in
constant time with `hmac.Equal`; without that check anyone who found the URL
could inject fake receipts. Then it acknowledges fast and works in the
background: Meta disables webhooks that answer slowly and OCR takes seconds, so
the handler returns 200 immediately and each image is ingested in its own
goroutine with its own timeout budget. Redeliveries are harmless, since the
unique index on `whatsappMediaId` turns a retry into a logged duplicate rather
than a second record.

With `WHATSAPP_PHONE_NUMBER_ID` set, every image gets a reply in the same chat
with what was extracted, so a bad parse shows up on your phone immediately
instead of after a database query.

```
*Receipt saved*

Merchant: WILLYS
Date: 2026-08-04
Total: 154.53 SEK
Items: 6

id: 6a759c94d7732138775c0dd8
```

Failures answer too, and say what to do about them: an expired media id asks for
the photo again, an unreadable image asks for better light. Sending needs no
message templates, because a reply always lands inside WhatsApp's 24-hour
customer service window.

### Correcting a receipt

Every receipt gets a short sequential number (`#7`), handed out by an atomic
`$inc` on a counters document rather than a document count, which races and
breaks after a deletion. That number is the handle for corrections sent as
ordinary WhatsApp messages:

```
edit 7 merchant: ICA
edit 7 total: 154,53, date: 2026-08-04
edit 7 merchant: ICA Kvantum Sundsvall, currency: SEK
```

Commas and colons are optional, case is ignored, and `154,53` is accepted
because that is how Swedish receipts print. The fields are `merchant`, `total`,
`subtotal`, `tax`, `currency`, and `date`. `help` lists them, and `stores` shows
what the bot has learned.

Edits are partial. `model.ReceiptUpdate` uses pointer fields, so "not mentioned"
and "set to empty" are different values and a merchant correction cannot blank
the total. They map onto a MongoDB `$set` of only the named keys, which avoids
the read-modify-write race a whole-document rewrite would have.

Naming a merchant also writes a row in the `stores` collection, keyed on the
shop's registration number scraped from the same OCR text
(`internal/receipt/orgnr.go`):

```
Orgnr 556163-2232        →  Willys
Org.nr. :556540-0081     →  ICA
```

The registration number is the key rather than the shop name, because the name
is the thing OCR gets wrong: it is usually a logo, and Tesseract does not read
logos. The number is plain digits in a fixed format, unique per company, and
printed on every Swedish receipt.

Later receipts from the same company are named from the directory at ingest,
before the insert, so the stored record is right the first time. When both OCR
and the directory have an opinion, `STORE_OVERRIDES_OCR` decides; the default
keeps the parsed name and lets the directory fill only a blank.

Text that is not a command is acknowledged and logged by shape only, never by
content. At scale, replace the per-image goroutine with a real queue.

The stored document uses camelCase keys (`whatsappMediaId`, `lineItems`,
`createdAt`). `date` is stored as a `YYYY-MM-DD` string rather than a BSON date,
because ISO-8601 sorts chronologically as text, so `$gte` and `$lte` range
queries work directly on it.

Responses share one envelope:

```jsonc
// success
{ "success": true,
  "data": { "id": "…", "merchant": "STARBUCKS", "total": 13.63, "date": "2025-08-04" },
  "meta": { "total": 1 } }

// failure
{ "success": false, "error": { "message": "validation failed",
                               "fields": { "user_id": "must not be empty" },
                               "request_id": "ZR4K…" } }
```

Status codes worth keeping apart:

- 400: the body was unusable (malformed JSON, wrong types, unknown fields)
- 404: no such resource, or no such route
- 405: the path exists but not for that method (with an `Allow` header)
- 422: the JSON parsed but the values broke a domain rule
- 500: something unexpected; details go to the logs, never to the client

---

## Configuration

Config comes from environment variables, with `.env` as a local convenience.
Two rules, both in `internal/config/dotenv.go`: a missing `.env` is not an error,
since production has none and the orchestrator injects real environment
variables instead; and real environment variables always win over `.env`, so a
stale checked-out file cannot override a deployed secret.

Everything is optional. Each integration boots only when its variable is set:

| Variable                    | Default        | Meaning                                    |
| --------------------------- | -------------- | ------------------------------------------ |
| `ATLAS`                     | *(unset)*      | MongoDB connection string; without it receipts are not persisted |
| `MONGO_DB`                  | `go_ocr`       | database name                              |
| `MONGO_RECEIPTS_COLLECTION` | `receipts`     | receipts collection                        |
| `MONGO_TIMEOUT`             | `10s`          | connect / server-selection timeout         |
| `WHATSAPP_TOKEN`            | *(unset)*      | Cloud API token; without it POST → 503     |
| `WHATSAPP_API_BASE`         | graph v21.0    | Graph API root, version pinned             |
| `WHATSAPP_VERIFY_TOKEN`     | *(unset)*      | your invented string for the webhook handshake |
| `WHATSAPP_APP_SECRET`       | *(unset)*      | Meta app secret; verifies webhook signatures |
| `WHATSAPP_PHONE_NUMBER_ID`  | *(unset)*      | business number id replies are sent from; without it receipts are ingested silently |
| `MONGO_STORES_COLLECTION`   | `stores`       | the learned registration-number to merchant directory |
| `MONGO_COUNTERS_COLLECTION` | `counters`     | sequence counters; what gives each receipt its short number |
| `STORE_OVERRIDES_OCR`       | `false`        | `true` lets a learned merchant beat the OCR-read one; `false` fills only a blank |
| `WHATSAPP_TIMEOUT`          | `20s`          | budget for the two media calls             |
| `MEDIA_MAX_BYTES`           | `10MB`         | max image size (accepts `10MB`, `512KB`)   |
| `TESSERACT_BIN`             | `tesseract`    | binary name or absolute path               |
| `TESSERACT_LANG`            | `eng`          | traineddata language, e.g. `eng+mon`       |
| `OCR_TIMEOUT`               | `30s`          | budget for one tesseract run               |
| `RECEIPT_DAY_FIRST`         | `true`         | `04/08/2025` → 4 Aug (`false` → 8 Apr)     |
| `RECEIPT_DEFAULT_CURRENCY`  | `USD`          | used when the receipt shows no symbol      |
| `DOTENV_PATH`               | `.env`         | where to look for the env file             |
| `APP_ENV`                   | `development`  | text logs locally, JSON otherwise          |
| `APP_ADDR`                  | `:8080`        | bind address                               |
| `READ_TIMEOUT`              | `5s`           | max time to read a request                 |
| `WRITE_TIMEOUT`             | `75s`          | max time to write a response               |
| `IDLE_TIMEOUT`              | `60s`          | keep-alive idle limit                      |
| `REQUEST_TIMEOUT`           | `60s`          | max handler runtime (must be < write)      |
| `SHUTDOWN_TIMEOUT`          | `15s`          | drain window on SIGTERM                    |
| `LOG_LEVEL`                 | `info`         | `debug` / `info` / `warn` / `error`        |

`REQUEST_TIMEOUT` is 60s rather than the 10s you would pick for a plain JSON API,
because a receipt upload downloads an image and then runs OCR inside that one
request. Startup fails if `WHATSAPP_TIMEOUT + OCR_TIMEOUT` does not fit inside
it, since otherwise every upload would die at the timeout handler with no clue
why.

A missing integration logs a warning at boot and its endpoints answer 503, while
everything else keeps working. You can run the core scanner before either
MongoDB or the WhatsApp side exists.

A value that is set but bad stops the process at startup rather than silently
falling back. A malformed `ATLAS` or an unreachable cluster fails the deploy
instead of producing a server that 500s on every request. Being absent is fine,
being wrong is fatal.

```bash
APP_ENV=production LOG_LEVEL=debug APP_ADDR=:9000 make run
```

The connection string is never logged. `Config.MongoURISafe()` runs it through
`net/url` and `User.Redacted()` first, so logs show the host but not the
password.
