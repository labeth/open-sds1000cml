# Repo-level conveniences over the two Go modules (app/ and ota/).
#
#   make test    — run both modules' full test suites (browser/node tests
#                  self-skip unless node + Playwright are installed; set
#                  CI_REQUIRE_BROWSER=1 to turn those skips into failures).
#                  With the browsers, the web package alone runs for about
#                  8 minutes, close to go test's 10-minute default timeout.

.PHONY: test

test:
	cd app && go test -timeout 30m ./...
	cd ota && go test ./...
