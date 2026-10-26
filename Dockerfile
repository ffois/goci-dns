# --- Build stage ---
FROM golang:1.27-alpine AS builder
LABEL authors="Flavio Fois"

WORKDIR /build

COPY go.mod go.sum ./
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

COPY docker/entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh && chmod +x /entrypoint.sh

RUN mkdir -p /config /data /logs

# The logger writes its rotating file under $XDG_CONFIG_HOME/goci-dns/logs.
ENV XDG_CONFIG_HOME=/logs

ENTRYPOINT ["/entrypoint.sh"]