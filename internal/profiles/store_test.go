package profiles

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"s3-backup/internal/backup"
)

type fakeVault struct {
	key    string
	err    error
	writes int
}

func (v *fakeVault) Get() (string, error) {
	if v.err != nil {
		return "", v.err
	}
	if v.key == "" {
		return "", ErrKeyNotFound
	}
	return v.key, nil
}
func (v *fakeVault) Set(key string) error {
	if v.err != nil {
		return v.err
	}
	v.key = key
	v.writes++
	return nil
}

func fixtureConfig() backup.Config {
	return backup.Config{Endpoint: "https://storage.yandexcloud.net", Region: "ru-central1", Bucket: "fixture", AccessKey: "fixture-access-123", SecretKey: "fixture-secret-456", SessionToken: "fixture-token-789", Destination: "/path with spaces/backup", Prefix: "images/", Parallel: 4, Compress: true, AWSPath: "/fixture/aws"}
}

func openFixture(t *testing.T, dir string, v Vault) *Store {
	t.Helper()
	s, err := Open(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLastAndProfilesSurviveRestartWithoutPlaintextKeys(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVault{}
	s := openFixture(t, dir, v)
	c := fixtureConfig()
	if _, err := s.SaveLast(c, ""); err != nil {
		t.Fatal(err)
	}
	result, err := s.SaveProfile("  Yandex / рабочий  ", c)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "Yandex / рабочий" || result.Warning != "" {
		t.Fatalf("wrong save result: %+v", result)
	}
	body, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{c.AccessKey, c.SecretKey, c.SessionToken, v.key} {
		if strings.Contains(string(body), secret) {
			t.Fatal("secret exposed in settings file")
		}
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Last.Secrets == "" || doc.Last.Config.AccessKey != "" || doc.Profiles[0].Config.SecretKey != "" {
		t.Fatal("credentials were not separated from settings")
	}
	if doc.Last.Secrets == "" || doc.Profiles[0].Secrets == "" {
		t.Fatal("missing encrypted credentials")
	}
	s = openFixture(t, dir, v)
	state := s.State()
	if state.Last == nil || !reflect.DeepEqual(*state.Last, c) || state.LastAlias != result.Name || state.Warning != "" {
		t.Fatalf("settings not restored: %+v", state)
	}
	loaded, err := s.LoadProfile(result.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Config, c) || loaded.Warning != "" {
		t.Fatal("profile did not restore all credentials and parameters")
	}
	if v.writes != 1 {
		t.Fatalf("encryption key unnecessarily regenerated %d times", v.writes)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("temporary settings files left behind")
	}
}

func TestProfileEditsRequireExplicitSaveAndDeletionKeepsLast(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVault{}
	s := openFixture(t, dir, v)
	original := fixtureConfig()
	if _, err := s.SaveProfile("Prod", original); err != nil {
		t.Fatal(err)
	}
	draft := original
	draft.Bucket = "draft"
	draft.SecretKey = "draft-secret"
	if _, err := s.SaveLast(draft, "Prod"); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadProfile("Prod")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config != original {
		t.Fatal("autosaving last params silently changed named profile")
	}
	result, err := s.SaveProfile("prod", draft)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "Prod" || len(result.Aliases) != 1 {
		t.Fatal("case-insensitive overwrite created duplicate")
	}
	if _, err := s.DeleteProfile("Prod"); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, dir, v)
	state := s.State()
	if len(state.Aliases) != 0 || state.LastAlias != "" || state.Last == nil || *state.Last != draft {
		t.Fatal("deletion did not keep independent last params")
	}
	if _, err := s.LoadProfile("Prod"); err == nil {
		t.Fatal("deleted profile still loads")
	}
}

func TestUnavailableVaultNeverStoresCredentialsInPlaintext(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVault{err: errors.New("vault locked")}
	s := openFixture(t, dir, v)
	c := fixtureConfig()
	result, err := s.SaveProfile("Offline", c)
	if err != nil {
		t.Fatal(err)
	}
	if result.Warning == "" {
		t.Fatal("missing warning about unavailable vault")
	}
	s = openFixture(t, dir, v)
	state := s.State()
	if state.Last == nil || state.Last.Bucket != c.Bucket || state.Last.SecretKey != "" || state.Last.AccessKey != "" || state.Last.SessionToken != "" {
		t.Fatal("unexpected fallback settings")
	}
	body, err := os.ReadFile(s.filename)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{c.AccessKey, c.SecretKey, c.SessionToken} {
		if strings.Contains(string(body), value) {
			t.Fatal("plaintext fallback exposed credentials")
		}
	}
}

func TestMissingKeyDoesNotEraseExistingEncryptedSettings(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVault{}
	s := openFixture(t, dir, v)
	c := fixtureConfig()
	if _, err := s.SaveProfile("Prod", c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.filename)
	if err != nil {
		t.Fatal(err)
	}
	key := v.key
	v.key = ""
	s = openFixture(t, dir, v)
	state := s.State()
	if state.Warning == "" || state.Last == nil || state.Last.SecretKey != "" {
		t.Fatal("missing key not reported")
	}
	if _, err := s.SaveLast(*state.Last, "Prod"); err == nil {
		t.Fatal("missing credentials silently erased existing ciphertext")
	}
	if _, err := s.SaveProfile("Prod", *state.Last); err == nil {
		t.Fatal("profile overwritten while encryption key inaccessible")
	}
	if _, err := s.SaveProfile("New", c); err == nil {
		t.Fatal("missing key replaced while existing settings still reference it")
	}
	after, err := os.ReadFile(s.filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("inaccessible ciphertext was altered")
	}
	v.key = key
	if restored := s.State(); restored.Warning != "" || restored.Last == nil || *restored.Last != c {
		t.Fatal("settings did not recover when vault unlocked")
	}
}

func TestCiphertextTamperingIsReported(t *testing.T) {
	dir := t.TempDir()
	v := &fakeVault{}
	s := openFixture(t, dir, v)
	if _, err := s.SaveLast(fixtureConfig(), ""); err != nil {
		t.Fatal(err)
	}
	encoded, err := base64.StdEncoding.DecodeString(s.data.Last.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)-1] ^= 0xff
	s.data.Last.Secrets = base64.StdEncoding.EncodeToString(encoded)
	state := s.State()
	if state.Warning == "" || state.Last.SecretKey != "" {
		t.Fatal("authenticated encryption did not reject modified credentials")
	}
}

func TestCorruptFileAndFailedWriteDoNotDestroySettings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(file, []byte("broken settings"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, &fakeVault{}); err == nil {
		t.Fatal("corrupt settings were accepted")
	}
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "broken settings" {
		t.Fatal("corrupt file was overwritten")
	}
	blocked := filepath.Join(t.TempDir(), "blocked")
	s := openFixture(t, blocked, &fakeVault{})
	if err := os.WriteFile(blocked, []byte("file instead of directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProfile("Prod", fixtureConfig()); err == nil {
		t.Fatal("write to invalid directory succeeded")
	}
	if len(s.State().Aliases) != 0 {
		t.Fatal("failed write mutated in-memory profiles")
	}
}

func TestAliasesAreValidatedAndSorted(t *testing.T) {
	s := openFixture(t, t.TempDir(), &fakeVault{})
	c := fixtureConfig()
	for _, name := range []string{"", "  ", "bad\nname", strings.Repeat("я", 81)} {
		if _, err := s.SaveProfile(name, c); err == nil {
			t.Fatalf("invalid alias accepted: %q", name)
		}
	}
	for _, name := range []string{"Zulu", "alpha", "Бакет"} {
		if _, err := s.SaveProfile(name, c); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(s.State().Aliases, []string{"alpha", "Zulu", "Бакет"}) {
		t.Fatal("profiles are not sorted")
	}
}
