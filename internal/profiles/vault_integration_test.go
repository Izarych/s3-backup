package profiles

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

// Opt-in native integration uses a separate credential target, never user profiles.
func TestSystemVaultEncryptedProfileRoundTrip(t *testing.T) {
	if os.Getenv("S3_BACKUP_TEST_KEYRING") != "1" {
		t.Skip("set S3_BACKUP_TEST_KEYRING=1 to test the current OS credential store")
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	v := SystemVault{service: service + ".test." + hex.EncodeToString(random[:])}
	t.Cleanup(func() { _ = keyring.Delete(v.name(), keyAccount) })
	dir := t.TempDir()
	s := openFixture(t, dir, v)
	c := fixtureConfig()
	result, err := s.SaveProfile("Native keyring fixture", c)
	if err != nil {
		t.Fatal(err)
	}
	if result.Warning != "" {
		t.Fatal("native keyring failed to store the encryption key")
	}
	s = openFixture(t, dir, v)
	state := s.State()
	if state.Warning != "" || state.Last == nil || *state.Last != c {
		t.Fatal("native credential store did not restore encrypted settings")
	}
}
