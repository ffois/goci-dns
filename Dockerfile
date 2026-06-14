# --- Build stage ---
FROM golang:alpine AS builder
LABEL authors="Flavio Fois"

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE}" \
    -o goci-dns .

# --- Runtime stage ---
FROM alpine:latest

LABEL authors="Flavio Fois"
ARG VERSION=dev
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.version=${VERSION}
LABEL org.opencontainers.image.created=${BUILD_DATE}

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/goci-dns .
COPY --from=builder /build/config.ini .

COPY docker/entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh && chmod +x /entrypoint.sh

RUN mkdir -p /logs

ENTRYPOINT ["/entrypoint.sh"]