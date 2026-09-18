FROM golang:1.26.2-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/api

FROM alpine:3.20

# tesseract is required at runtime; without it every scan returns 503.
# swe as well as eng because the receipts are Swedish; TESSERACT_LANG defaults
# to swe+eng and startup logs an error if either is missing.
RUN apk --no-cache add \
      ca-certificates \
      tesseract-ocr \
      tesseract-ocr-data-eng \
      tesseract-ocr-data-swe \
 && adduser -D -H -u 10001 app

WORKDIR /app

COPY --from=builder /app/main .

USER app

EXPOSE 8080

CMD ["./main"]
