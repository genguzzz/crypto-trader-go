# crypto-trader-go: recorder + scanner (public OKX data only)
.PHONY: web build test run arm64 clean

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

# Home NAS / N1 (iStoreOS, aarch64); pure-Go SQLite, no CGO.
arm64: web
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/ctg-linux-arm64 ./cmd/server

clean:
	rm -rf bin web/dist/assets web/dist/index.html
