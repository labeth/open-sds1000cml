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
  -X github.com/labeth/open-sds1000cml/app/internal/buildinfo.Version=$(VERSION) \
  -X github.com/labeth/open-sds1000cml/app/internal/buildinfo.Commit=$(COMMIT) \
  -X github.com/labeth/open-sds1000cml/app/internal/buildinfo.Built=$(BUILT)

DIST := dist
# The nine shipping FPGA images (fpga/bitstreams, built by fpga/build.ts), each
# embedded as internal/fpgaload/<name>.rbf. To try a new FPGA build, point one
# at it (PACKET_RBF=...): a test build, not a shipping one. Every image is
# required: the app has one way to run, with all its images.
RBF         ?= ../fpga/bitstreams/general.rbf
STACK_RBF   ?= ../fpga/bitstreams/stacking.rbf
PACKET_RBF  ?= ../fpga/bitstreams/packet.rbf
LINE_RBF    ?= ../fpga/bitstreams/line.rbf
FRAME_RBF   ?= ../fpga/bitstreams/frame.rbf
STREAM_RBF  ?= ../fpga/bitstreams/stream.rbf
BITS_RBF    ?= ../fpga/bitstreams/bits.rbf
QUALIFY_RBF ?= ../fpga/bitstreams/qualify.rbf
FAST_RBF    ?= ../fpga/bitstreams/fast.rbf
# embedded name : source
IMAGES := general:$(RBF) stack:$(STACK_RBF) packet:$(PACKET_RBF) line:$(LINE_RBF) frame:$(FRAME_RBF) \
  stream:$(STREAM_RBF) bits:$(BITS_RBF) qualify:$(QUALIFY_RBF) fast:$(FAST_RBF)
TAGS := $(foreach i,$(IMAGES),with$(firstword $(subst :, ,$(i)))bitstream)

.PHONY: all app app-release test vet clean

all: app

# app: the ARMv7 static binary with every FPGA image embedded. It loads the
# general image at startup with LCD progress and verifies the SRAM transport
# before acquiring. Refuses to build without an image or with one of the wrong
# size: an EP4CE10 image is 368011 bytes uncompressed, and from a quarter of
# that up compressed (fpgaload.validLen; the images ship compressed, which
# halves an image switch).
app:
	@for i in $(IMAGES); do n=$${i%%:*}; f=$${i#*:}; \
	  test -f "$$f" || { echo "app: missing $$f - build it with fpga/build.ts"; exit 1; }; \
	  s=$$(stat -c %s "$$f"); test "$$s" -eq 368011 || { test "$$s" -ge 92002 && test "$$s" -lt 368011; } || \
	    { echo "app: $$f is $$s bytes, not an EP4CE10 .rbf"; exit 1; }; \
	  cp "$$f" internal/fpgaload/$$n.rbf; done
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(DIST)/app-arm ./cmd/app
	@echo "app: $(DIST)/app-arm embeds $(foreach i,$(IMAGES),$(lastword $(subst :, ,$(i))))"

# app-release: the same build (the name the stick and release scripts use).
app-release: app

test:
	go test -timeout 30m ./...

vet:
	go vet ./...

clean:
	rm -rf $(DIST) $(foreach i,$(IMAGES),internal/fpgaload/$(firstword $(subst :, ,$(i))).rbf)
