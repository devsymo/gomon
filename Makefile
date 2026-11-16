VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web build test vet dev-web dev-db docker clean

all: web build

web:
	cd web && npm ci --no-audit --no-fund && npm run build && touch dist/.gitkeep

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/gomon ./cmd/gomon

test:
	go test ./...
	cd web && npx tsc --noEmit -p .

vet:
	go vet ./...

# Frontend dev server with hot reload, proxying /api to a running gomon on :8080
dev-web:
	cd web && npm run dev

# Throwaway Postgres for development
dev-db:
	docker run --rm -d --name gomon-dev-pg -e POSTGRES_USER=gomon -e POSTGRES_PASSWORD=gomon -e POSTGRES_DB=gomon -p 5432:5432 postgres:17-alpine

docker:
	docker build --build-arg VERSION=$(VERSION) -t gomon:$(VERSION) -t gomon:latest .

clean:
	rm -rf bin web/dist/assets web/dist/index.html web/dist/favicon.svg
