# --- Build stage ---
FROM golang:alpine AS builder
LABEL authors="Flavio Fois"

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o goci-dns .

# --- Runtime stage ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/goci-dns .
COPY --from=builder /build/config.ini .

COPY docker/entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh && chmod +x /entrypoint.sh

RUN mkdir -p /logs

ENTRYPOINT ["/entrypoint.sh"]