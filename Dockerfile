# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /otun-server ./cmd/server

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

COPY --from=builder /otun-server /usr/local/bin/otun-server

EXPOSE 4443 8080 8081

CMD ["otun-server", "-control", ":4443", "-public", ":8080", "-check", ":8081"]
