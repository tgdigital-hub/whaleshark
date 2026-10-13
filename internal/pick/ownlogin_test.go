package pick

import (
	"testing"

	"github.com/tgdigital-hub/whaleshark/test/ownlogin"
)

// No test of this package is in the login of whoever runs it.
func TestMain(m *testing.M) { ownlogin.Main(m) }
