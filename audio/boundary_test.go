package audio_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Audits every Go source file, including inactive architecture paths. External
// runtime dependencies require an explicit review here before admission.
func TestAudioImportBoundary(t *testing.T) {
	allowed := map[string]bool{"context": true, "encoding/binary": true, "errors": true, "fmt": true, "io": true, "math": true, "math/bits": true, "os": true}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, e := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if e != nil {
			return e
		}
		for _, imp := range f.Imports {
			s, e := strconv.Unquote(imp.Path.Value)
			if e != nil {
				return e
			}
			if !allowed[s] && !strings.HasPrefix(s, "github.com/rcarmo/go-264/audio/") {
				t.Errorf("unreviewed audio import %s in %s", s, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
