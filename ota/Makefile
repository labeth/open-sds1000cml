# open-sds OTA — build the device agent (ARMv7 static) and host otactl,
# and lay out a USB stick tree under dist/.

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
  -X open-sds/ota/internal/buildinfo.Version=$(VERSION) \
  -X open-sds/ota/internal/buildinfo.Commit=$(COMMIT) \
  -X open-sds/ota/internal/buildinfo.Built=$(BUILT)

DIST := dist

.PHONY: all agent otactl stubapp test vet dist clean

all: agent otactl stubapp

agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/agent-arm ./cmd/agent

# stubapp: the reference clean-room app (ARMv7) — proves the app<->OTA contract.
stubapp:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/stubapp-arm ./cmd/stubapp

otactl:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/otactl ./cmd/otactl

test:
	go test ./...

vet:
	go vet ./...

# Lay out the USB stick tree under dist/stick (copy to the real stick after).
dist: agent otactl
	rm -rf $(DIST)/stick
	mkdir -p $(DIST)/stick/ota/logs $(DIST)/stick/agent-slots/A $(DIST)/stick/agent-slots/B \
	         $(DIST)/stick/agent-slots/emergency $(DIST)/stick/agent-slots/staging
	cp boot/startup.sh $(DIST)/stick/startup.sh
	cp boot/commands   $(DIST)/stick/commands
	cp boot/agent.env.example $(DIST)/stick/ota/agent.env
	cp $(DIST)/agent-arm $(DIST)/stick/ota/agent.A
	cp $(DIST)/agent-arm $(DIST)/stick/ota/agent.B
	printf 'A\n' > $(DIST)/stick/ota/agent.active
	printf 'A\n' > $(DIST)/stick/ota/agent.confirmed
	@echo "stick tree ready under $(DIST)/stick — review ota/agent.env, then copy to the USB device"

clean:
	rm -rf $(DIST)
