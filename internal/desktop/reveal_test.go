package desktop

import (
	"path/filepath"
	"testing"
)

func TestRevealHandlesSpacesWithoutShellParsing(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "folder with spaces", "backup ' sample.zip")
	for _, platform := range []string{"windows", "darwin", "linux"} {
		program, args, err := revealCommand(platform, filename)
		if err != nil || program == "" {
			t.Fatalf("%s: %v", platform, err)
		}
		last := args[len(args)-1]
		want := filename
		if platform == "linux" {
			want = filepath.Dir(filename)
		}
		if last != want {
			t.Fatalf("%s: path was modified: %q", platform, last)
		}
	}
	if err := RevealArchive(""); err == nil {
		t.Fatal("empty archive path accepted")
	}
}
