package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpaceCheckUsesDestinationFilesystem(t *testing.T) {
	dir := t.TempDir()
	free, err := availableSpace(dir)
	if err != nil || free == 0 {
		t.Fatalf("cannot query destination volume: free=%d err=%v", free, err)
	}
	if _, err := availableSpace(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing destination unexpectedly succeeded")
	}
}

func TestAWSDiscoveryIncludesDesktopInstallLocations(t *testing.T) {
	base := t.TempDir()
	executable := filepath.Join(base, "S3 Backup.app", "Contents", "MacOS", "s3-backup")
	candidates := awsCandidates("darwin", executable, base, "", "")
	want := []string{filepath.Join(base, "aws-cli", "aws"), filepath.Join(base, "S3 Backup.app", "Contents", "Resources", "aws-cli", "aws"), "/usr/local/bin/aws", "/opt/homebrew/bin/aws", filepath.Join(base, ".local", "bin", "aws")}
	for _, path := range want {
		found := false
		for _, candidate := range candidates {
			if filepath.Clean(candidate) == filepath.Clean(path) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing desktop CLI location: %s", path)
		}
	}
}

func TestExplicitAWSPathIsValidated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-aws")
	if _, err := FindAWS(missing); err == nil {
		t.Fatal("invalid explicit CLI path was accepted")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := FindAWS(executable); err != nil || got != executable {
		t.Fatalf("explicit executable path failed: %s %v", got, err)
	}
}
