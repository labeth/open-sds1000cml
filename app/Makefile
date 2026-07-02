# open-sds app — the clean-room scope application (ARMv7 static).
# Deploy with: otactl -tcp <device>:5900 update-app dist/app-arm

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X open-sds/app/internal/buildinfo.Version=$(VERSION) \
  -X open-sds/app/internal/buildinfo.Commit=$(COMMIT) \
  -X open-sds/app/internal/buildinfo.Built=$(BUILT)

DIST := dist

.PHONY: all app test vet clean

all: app

app:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(DIST)
