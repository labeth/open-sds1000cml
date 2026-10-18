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
# The shipping FPGA images (fpga/bitstreams, built by fpga/build.ts). Override
# one with e.g. PACKET_RBF=... to test a new build; an empty value leaves that
# image out of the binary.
RBF ?= ../fpga/bitstreams/general.rbf
EMBED := internal/fpgaload/general.rbf
# Stacking image (ADR-STACKING-IMAGE-SPLIT): FPGA super-resolution stacking.
STACK_RBF ?= ../fpga/bitstreams/stacking.rbf
STACK_EMBED := internal/fpgaload/stack.rbf
# Packet image: ARINC 429, CAN, FlexRay, MIL-1553 and SENT triggers.
PACKET_RBF ?= ../fpga/bitstreams/packet.rbf
PACKET_EMBED := internal/fpgaload/packet.rbf
# Line image: Manchester and USB low-speed triggers.
LINE_RBF ?= ../fpga/bitstreams/line.rbf
LINE_EMBED := internal/fpgaload/line.rbf
# Stream image (ADR-STREAM-IMAGE): continuous capture for roll mode. Optional
# until it ships in fpga/bitstreams.
STREAM_RBF ?= $(wildcard ../fpga/bitstreams/stream.rbf)
STREAM_EMBED := internal/fpgaload/stream.rbf
# Qualifier image (ADR-HARDWARE-QUALIFIERS): pulse, slope and video triggers
# decided in the FPGA. Optional until it ships in fpga/bitstreams.
QUALIFY_RBF ?= $(wildcard ../fpga/bitstreams/qualify.rbf)
QUALIFY_EMBED := internal/fpgaload/qualify.rbf
# SPI on the samples (ADR-SAMPLE-DOMAIN-SPI). Optional until it ships in
# fpga/bitstreams.
FAST_RBF ?= $(wildcard ../fpga/bitstreams/fast.rbf)
FAST_EMBED := internal/fpgaload/fast.rbf

.PHONY: all app app-release test vet clean

all: app

# app: the ARMv7 static binary WITHOUT the bitstream. It verifies the fabric
# identity and runs only against a preloaded general SRAM image.
app:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app

# rbfsize: the image's size is an EP4CE10 .rbf's, uncompressed or compressed
rbfsize = s=$$(stat -c %s "$(1)"); test "$$s" -eq 368011 || { test "$$s" -ge 92002 && test "$$s" -lt 368011; }

# app-release: embeds the canonical general image and loads it at startup with
# visible LCD progress, then verifies SRAM transport before acquiring. Refuses to build
# without the .rbf (an empty embed would be a silent verify-only binary) or
# with an .rbf of the wrong size: an EP4CE10 image is 368011 bytes
# uncompressed, and from a quarter of that up compressed (fpgaload.validLen;
# the images ship compressed, which halves an image switch).
app-release:
	@test -f "$(RBF)" || { echo "app-release: missing $(RBF) — build it with fpga/build.ts"; exit 1; }
	@$(call rbfsize,$(RBF)) || { echo "app-release: $(RBF) is $$(stat -c %s "$(RBF)") bytes, not an EP4CE10 .rbf (368011 uncompressed, 92002..368010 compressed)"; exit 1; }
	cp "$(RBF)" $(EMBED)
	@if [ -n "$(STACK_RBF)" ]; then \
	  $(call rbfsize,$(STACK_RBF)) || { echo "app-release: $(STACK_RBF) is not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$(STACK_RBF)" $(STACK_EMBED); \
	else rm -f $(STACK_EMBED); fi
	@if [ -n "$(PACKET_RBF)" ]; then \
	  $(call rbfsize,$(PACKET_RBF)) || { echo "app-release: $(PACKET_RBF) is not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$(PACKET_RBF)" $(PACKET_EMBED); \
	else rm -f $(PACKET_EMBED); fi
	@if [ -n "$(LINE_RBF)" ]; then \
	  $(call rbfsize,$(LINE_RBF)) || { echo "app-release: $(LINE_RBF) is not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$(LINE_RBF)" $(LINE_EMBED); \
	else rm -f $(LINE_EMBED); fi
	@if [ -n "$(STREAM_RBF)" ]; then \
	  $(call rbfsize,$(STREAM_RBF)) || { echo "app-release: $(STREAM_RBF) is not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$(STREAM_RBF)" $(STREAM_EMBED); \
	else rm -f $(STREAM_EMBED); fi
	@if [ -n "$(QUALIFY_RBF)" ]; then \
	  $(call rbfsize,$(QUALIFY_RBF)) || { echo "app-release: $(QUALIFY_RBF) is not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$(QUALIFY_RBF)" $(QUALIFY_EMBED); \
	else rm -f $(QUALIFY_EMBED); fi
	@if [ -n "$(FAST_RBF)" ]; then \
	  $(call rbfsize,$(FAST_RBF)) || { echo "app-release: $(FAST_RBF) is not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$(FAST_RBF)" $(FAST_EMBED); \
	else rm -f $(FAST_EMBED); fi
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -tags "withgeneralbitstream$(if $(STACK_RBF), withstackbitstream)$(if $(PACKET_RBF), withpacketbitstream)$(if $(LINE_RBF), withlinebitstream)$(if $(STREAM_RBF), withstreambitstream)$(if $(QUALIFY_RBF), withqualifybitstream)$(if $(FAST_RBF), withfastbitstream)" -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app
	@echo "app-release: $(DIST)/app-arm embeds $(RBF) ($$(sha256sum "$(RBF)" | cut -c1-16)…)$(if $(STACK_RBF), stacking image $(STACK_RBF))$(if $(PACKET_RBF), packet image $(PACKET_RBF))$(if $(LINE_RBF), line image $(LINE_RBF))$(if $(STREAM_RBF), stream image $(STREAM_RBF))$(if $(QUALIFY_RBF), qualifier image $(QUALIFY_RBF))$(if $(FAST_RBF), fast image $(FAST_RBF))"

test:
	go test -timeout 30m ./...

vet:
	go vet ./...

clean:
	rm -rf $(DIST) $(EMBED) $(STACK_EMBED) $(PACKET_EMBED) $(LINE_EMBED) $(STREAM_EMBED) $(QUALIFY_EMBED) $(FAST_EMBED)
