FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/currency-converter-bot ./cmd/bot

FROM alpine:3.22

RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S app -G app
WORKDIR /app

COPY --from=builder /out/currency-converter-bot /app/currency-converter-bot
RUN mkdir -p /app/data && chown -R app:app /app

USER app
VOLUME ["/app/data"]

ENTRYPOINT ["/app/currency-converter-bot"]
