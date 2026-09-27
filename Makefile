# crypto-trader-go: recorder + scanner (public OKX data only)
.PHONY: web build test run linux deploy clean

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	go build -o bin/ctg ./cmd/server

test:
	go vet ./cmd/... ./internal/... ./web/...
	go test -race ./internal/...
	cd web && npm run typecheck

run: build
	./bin/ctg -addr 127.0.0.1:8686 -db data/ctg.db

# Static linux binary (pure-Go SQLite, no CGO) used by the server container.
linux: web
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/ctg-linux-amd64 ./cmd/server

# Build, upload, hot-swap and health-check on the server (tyo) (see docs/operations.md).
deploy:
	deploy/deploy.sh

clean:
	rm -rf bin web/dist/assets web/dist/index.html
