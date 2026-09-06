package release

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkCopyTree measures the release scratch-copy primitive on a
// realistic tree (the template's app dir when present, else a generated
// tree). copyTree already skips node_modules/.gradle/build — this records
// what the remaining copy costs so hardlinking (plan item #12) must beat it.
func BenchmarkCopyTree(b *testing.B) {
	src := "../template/app"
	if _, err := os.Stat(src); err != nil {
		src = b.TempDir()
		for i := 0; i < 50; i++ {
			f := filepath.Join(src, "file"+string(rune('a'+i%26))+".tsx")
			_ = os.WriteFile(f, []byte("export default function X() { return null; }\n"), 0o644)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := filepath.Join(b.TempDir(), "scratch")
		if err := copyTree(src, dst); err != nil {
			b.Fatal(err)
		}
	}
}
