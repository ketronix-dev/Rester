# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build Wasm Client
RUN GOOS=js GOARCH=wasm go build -o web/app.wasm ./cmd/frontend

# Build Backend Server
RUN go build -o rester-server ./cmd/backend

# Final Stage
FROM alpine:latest

WORKDIR /app

# Install dependencies (SSH, Restic) for demonstration
RUN apk add --no-cache openssh-client restic

# Copy binary and web assets (including the wasm file)
COPY --from=builder /app/rester-server .
COPY --from=builder /app/web ./web

EXPOSE 8000

CMD ["./rester-server"]
