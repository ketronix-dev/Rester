#!/bin/bash
set -e

echo "Building Frontend (Wasm)..."
GOOS=js GOARCH=wasm go build -o web/app.wasm ./cmd/frontend

echo "Building Backend..."
go build -o rester-server ./cmd/backend

echo "Starting Server..."
echo "Open http://localhost:8000 in your browser"
./rester-server
