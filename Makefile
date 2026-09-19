# Load .env (copy .env.example first). `export` passes every variable to the commands below.
-include .env
export

DB_URL ?= $(or $(DATABASE_URL),postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable)

.PHONY: up down proto migrate-up migrate-down run-api run-worker build test vet fmt dev dev-token lan-ip

up:
	docker compose up -d

down:
	docker compose down

proto:
	cd proto && buf generate

migrate-up:
	migrate -path migrations -database "$(DB_URL)" up

migrate-down:
	migrate -path migrations -database "$(DB_URL)" down -all

run-api:
	go run ./cmd/api

# One command for a fresh checkout: infra + schema, then run the API.
dev: up migrate-up run-api

# Print a user id + access token for the mobile app's "Developer sign-in".
#   make dev-token                      → dev@hivemind.local (created if missing)
#   make dev-token EMAIL=me@x.com ONBOARDED=1
dev-token:
	@go run ./cmd/devtoken $(if $(EMAIL),-email $(EMAIL)) $(if $(ONBOARDED),-onboarded)

# The address a phone on the same Wi-Fi should use as API_HOST.
lan-ip:
	@hostname -I | awk '{print $$1}'

run-worker:
	go run ./cmd/worker

build:
	go build ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

test:
	go test ./...
