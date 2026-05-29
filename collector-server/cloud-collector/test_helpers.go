package cloud

// added a test change which does absolutely nothing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
)

// LoadEnvFromFile loads environment variables from .env file for testing.
// It searches for go.mod by walking up the directory tree because tests can be
// run from any subdirectory (e.g., providers/aws/), but .env is always at the
// module root alongside go.mod.

func LoadEnvFromFile(t *testing.T) {
	path, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}

	// Walk up to find go.mod (module root)
	for {
		goModPath := filepath.Join(path, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			envPath := filepath.Join(path, ".env")
			if err := godotenv.Load(envPath); err != nil {
				t.Logf("Could not load .env file from %s: %v", envPath, err)
			}
			return
		}

		parent := filepath.Dir(path)
		if parent == path {
			t.Fatalf("Could not find go.mod in any parent directory.")
			return
		}
		path = parent
	}
}
