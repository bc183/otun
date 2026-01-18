# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /otun-server ./cmd/server

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

COPY --from=builder /otun-server /usr/local/bin/otun-server

# Create certs directory
RUN mkdir -p /var/lib/otun/certs

EXPOSE 4443 443 80

# Default: HTTP-only mode. Override with -domain for TLS.
CMD ["otun-server", "-control", ":4443", "-http", ":80"]
