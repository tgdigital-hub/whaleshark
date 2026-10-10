package install

import (
	"strings"
	"testing"
)

// Both scripts are in the program, and each is one a plain sh can run.
func TestTheScriptsAreCompiledIn(t *testing.T) {
	for name, text := range map[string]string{"server.sh": Server, "install.sh": Installer} {
		if !strings.HasPrefix(text, "#!/bin/sh\n") {
			t.Errorf("%s starts with %.20q", name, text)
		}
	}
}
