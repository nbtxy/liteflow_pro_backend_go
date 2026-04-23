.PHONY: build run build-local run-local dev test clean migrate db-up db-down db-logs

APP_NAME=liteflow-backend
BUILD_DIR=./build

build:
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/server

run: build
	$(BUILD_DIR)/$(APP_NAME)

build-local:
	go build -o $(BUILD_DIR)/$(APP_NAME)-local ./cmd/server

run-local: build-local
	$(BUILD_DIR)/$(APP_NAME)-local

dev:
	go run ./cmd/server

test:
	go test ./... -v -count=1

clean:
	rm -rf $(BUILD_DIR)

migrate-up:
	go run ./cmd/server -migrate-only

lint:
	golangci-lint run ./...

sqlc:
	sqlc generate

# Database (Docker)
db-up:
	docker compose up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@until docker compose exec postgres pg_isready -U liteflow -d liteflow > /dev/null 2>&1; do sleep 1; done
	@echo "PostgreSQL is ready on localhost:5432"

db-down:
	docker compose down

db-logs:
	docker compose logs -f postgres

db-reset:
	docker compose down -v
	docker compose up -d postgres
	@echo "Database reset complete"

# Docker (application)
docker-build:
	docker build -t $(APP_NAME) .

docker-run:
	docker run -d -p 8081:8081 --env-file .env --add-host=host.docker.internal:host-gateway -e DB_HOST=host.docker.internal $(APP_NAME)
