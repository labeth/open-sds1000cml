# Repo-level conveniences over the two Go modules (app/ and ota/).
#
#   make test    — run both modules' full test suites (browser/node tests
#                  self-skip unless node + Playwright are installed; set
#                  CI_REQUIRE_BROWSER=1 to turn those skips into failures).
#                  With the browsers, the web package alone runs for about
#                  8 minutes, close to go test's 10-minute default timeout.
#   make demo        — the browser demo as a static site in build/demo/ (the
#                  firmware as WebAssembly against a simulated board, built
#                  by open-dso-core from app/profile.yaml and demo/demo.json);
#                  serve the directory on localhost or https to try it
#   make demo-native — the same demo as a local web server (bench page at
#                  http://127.0.0.1:8090/demo/, web UI at /); Ctrl-C stops
#
# The demo is built by the core checkout next to this repository (the
# app's go.mod replace); CORE names another.

CORE ?= $(abspath ../open-dso-core)
DEMO_OUT ?= $(abspath build/demo)

.PHONY: test demo demo-native

demo:
	rm -rf $(DEMO_OUT)
	cd $(CORE) && go run ./cmd/dsodemo-site -profile $(abspath app/profile.yaml) -demo $(abspath demo/demo.json) -out $(DEMO_OUT)

demo-native:
	cd $(CORE) && go run ./cmd/dsodemo -profile $(abspath app/profile.yaml) -demo $(abspath demo/demo.json)

test:
	cd app && go test -timeout 30m ./...
	cd ota && go test ./...
