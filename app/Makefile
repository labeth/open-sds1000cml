# open-sds app — the clean-room scope application (ARMv7 static).
# Deploy with: otactl -tcp <device>:5900 update-app dist/app-arm

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
# Honor SOURCE_DATE_EPOCH (reproducible-builds.org): with it set, two builds of
# the same tree stamp the same Built and hash bit-identically.
# `date -d @sec` is GNU, `date -r sec` is BSD/macOS — try both.
ifdef SOURCE_DATE_EPOCH
BUILT ?= $(shell date -u -d "@$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -r "$(SOURCE_DATE_EPOCH)" +%Y-%m-%dT%H:%M:%SZ)
else
BUILT ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
endif
LDFLAGS := -s -w \
  -X open-sds/app/internal/buildinfo.Version=$(VERSION) \
  -X open-sds/app/internal/buildinfo.Commit=$(COMMIT) \
  -X open-sds/app/internal/buildinfo.Built=$(BUILT)

DIST := dist
# The general SRAM/triggering image, built by `cd ../fpga && make bitstream`.
RBF ?= ../fpga/acq_sram/out/general/current/bench.rbf
EMBED := internal/fpgaload/general.rbf

.PHONY: all app app-release test vet clean

all: app

# app: the ARMv7 static binary WITHOUT the bitstream. It verifies the fabric
# identity and runs only against a preloaded general SRAM image.
app:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app

# app-release: embeds the canonical general image and loads it at startup with
# visible LCD progress, then verifies SRAM transport before acquiring. Refuses to build
# without the .rbf (an empty embed would be a silent verify-only binary) or
# with an .rbf of the wrong size (368011 bytes = uncompressed EP4CE10).
app-release:
	@test -f "$(RBF)" || { echo "app-release: missing $(RBF) — build the fabric first (cd ../fpga && make bitstream)"; exit 1; }
	@test "$$(stat -c %s "$(RBF)")" = 368011 || { echo "app-release: $(RBF) is $$(stat -c %s "$(RBF)") bytes, want 368011 (uncompressed EP4CE10 .rbf)"; exit 1; }
	cp "$(RBF)" $(EMBED)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -tags withgeneralbitstream -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app
	@echo "app-release: $(DIST)/app-arm embeds $(RBF) ($$(sha256sum "$(RBF)" | cut -c1-16)…)"

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(DIST) $(EMBED)
