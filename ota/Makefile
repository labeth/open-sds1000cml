# open-sds OTA — build the device agent (ARMv7 static) and host otactl,
# and lay out a USB stick tree under dist/.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT   ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X open-sds/ota/internal/buildinfo.Version=$(VERSION) \
  -X open-sds/ota/internal/buildinfo.Commit=$(COMMIT) \
  -X open-sds/ota/internal/buildinfo.Built=$(BUILT)

DIST := dist

.PHONY: all agent otactl test vet dist clean

all: agent otactl

agent:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
	  go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/agent-arm ./cmd/agent

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
