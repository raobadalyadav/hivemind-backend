DB_URL ?= postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable

.PHONY: up down proto migrate-up migrate-down run-api run-worker build test vet fmt

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
