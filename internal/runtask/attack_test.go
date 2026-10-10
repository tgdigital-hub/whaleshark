package runtask_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// A repository that brings an approval of its own along, written into the
// project's folder of ours with the hash of its own settings, has approved
// nothing: its setup line is not run. The login keeps what a person approved.
func TestARepositorysOwnApprovalCountsForNothing(t *testing.T) {
	h := open(t, map[string]string{"A.1": work("A")})
	write(t, filepath.Join(h.p.Root, "whaleshark.toml"), "[setup]\nscript = \"echo ran>ran.txt\"\nscript_windows = \"echo ran>ran.txt\"\n")
	lines, err := contract.TrustLines(h.p.Root)
	if err != nil || len(lines) != 2 {
		t.Fatalf("the lines to approve: %q, %v", lines, err)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	forged, _ := json.Marshal(map[string]any{"version": contract.FileVersion, "hash": hex.EncodeToString(sum[:]), "lines": lines, "at": contract.Now()})
	write(t, filepath.Join(h.p.Root, contract.ProjectDir, "trust.json"), string(forged))
	h.add(map[string]string{"A": "none"}, "A")
	s, _ := h.p.Record()
	w := s.Tasks["A"].Worktree
	if _, err := os.Stat(filepath.Join(w.Path, "ran.txt")); err == nil || w.Setup != contract.SetupUntrusted {
		t.Fatalf("the setup line of a repository that approved itself: %s, ran.txt %v", w.Setup, err)
	}
}
