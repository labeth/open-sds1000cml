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

# The owned standard bitstream and where -tags withbitstream embeds it. The .rbf
# is a hardware build artifact (not committed); build it with `(cd ../fpga && make
# bitstream)`.
RBF   := ../fpga/standard/acq.rbf
EMBED := internal/fpgaload/acq.rbf

.PHONY: all app app-release test vet clean

all: app

# app: the default binary. Carries NO bitstream — it verifies the fabric build-ID
# and drives it, but cannot configure a cold-boot (factory) fabric. Use for a
# scope already carrying the standard build, or with SCOPE_SKIP_FPGA_LOAD=1.
app:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app

# app-release: the shipping binary. Embeds the standard bitstream so it
# reconfigures a cold-boot fabric to the owned build (method B) at startup.
# Requires the bitstream built first.
app-release: $(RBF)
	cp $(RBF) $(EMBED)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -tags withbitstream -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app
	rm -f $(EMBED)

$(RBF):
	@echo "missing $(RBF) — build the bitstream first: (cd ../fpga && make bitstream)"; exit 1

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(DIST) $(EMBED)
