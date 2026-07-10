# Repo-level conveniences over the two Go modules (app/ and ota/).
#
#   make test    — run both modules' full test suites (browser/node tests
#                  self-skip unless node + Playwright are installed; set
#                  CI_REQUIRE_BROWSER=1 to turn those skips into failures).

.PHONY: test

test:
	cd app && go test ./...
	cd ota && go test ./...
