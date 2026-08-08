package ui

import "testing"

func TestInjectedReleaseTagPrecedesBuildRevision(t *testing.T) {
	previous := buildVersion
	buildVersion = "v1.2.3"
	t.Cleanup(func() { buildVersion = previous })
	if got := detectGitVersion(); got != "v1.2.3" {
		t.Fatalf("detectGitVersion()=%q, want release tag", got)
	}
}
