package recommendation

import "testing"

// TestInitialScanJobNames keeps initialScanJobNames in lockstep with the
// "nb" (server-side) entries of recommendationJobProviderMap: every initial-scan
// job must be an nb job, and every nb job must be in the initial-scan set. This
// fails the build if a new nb scanner is added but not wired into first-connect.
func TestInitialScanJobNames(t *testing.T) {
	inInitial := make(map[string]bool, len(initialScanJobNames))
	for _, name := range initialScanJobNames {
		if inInitial[name] {
			t.Errorf("initialScanJobNames contains duplicate %q", name)
		}
		inInitial[name] = true

		provider, ok := recommendationJobProviderMap[name]
		if !ok {
			t.Errorf("initial-scan job %q is not in recommendationJobProviderMap", name)
			continue
		}
		if provider != "nb" {
			t.Errorf("initial-scan job %q has provider %q, want \"nb\"", name, provider)
		}
	}

	for name, provider := range recommendationJobProviderMap {
		if provider == "nb" && !inInitial[name] {
			t.Errorf("nb job %q is missing from initialScanJobNames", name)
		}
	}
}
