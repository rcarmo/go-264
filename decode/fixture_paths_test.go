package decode

import (
	"os"
	"path/filepath"
)

// External fixture assets are retained outside disposable project scratch.
func externalFixture(name string) string {
	root := os.Getenv("GO264_FIXTURE_ROOT")
	if root == "" {
		root = "/workspace/reports/go-264/fixtures"
	}
	return filepath.Join(root, name)
}
