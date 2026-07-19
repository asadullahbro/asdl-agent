# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o asdl-agent cmd/agent/main.go

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates bash

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/asdl-agent .
COPY config.yaml .

# Create work directory
RUN mkdir -p /tmp/asdl

# Run
ENTRYPOINT ["./asdl-agent"]
CMD ["-config", "config.yaml"]