// ENGMODEL-OWNER-UNIT: FU-APP-WEB

package webscope

import (
	"os"
	"testing"

	"github.com/labeth/open-dso-core/webclient"
)

// pkgDir is this package's own directory, for tests that hand a harness one
// of this scope's source files.
var pkgDir string

// TestMain runs the tests from the shared web client's directory, where the
// node and browser harnesses and the JS modules they load are.
// TRLC-LINKS: REQ-SDS-161
func TestMain(m *testing.M) {
	var err error
	if pkgDir, err = os.Getwd(); err != nil {
		panic(err)
	}
	if err := os.Chdir(webclient.Dir()); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
