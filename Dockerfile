FROM golang:1.26.2-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/api

FROM alpine:3.20

# tesseract is required at runtime; without it every scan returns 503.
RUN apk --no-cache add \
      ca-certificates \
      tesseract-ocr \
      tesseract-ocr-data-eng

WORKDIR /root/

COPY --from=builder /app/main .

EXPOSE 8080

CMD ["./main"]
