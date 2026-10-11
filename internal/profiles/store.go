package profiles

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"s3-backup/internal/backup"
)

const service = "io.s3backup.desktop"
const keyAccount = "profile-encryption-v1"
const associatedData = "s3-backup/profiles/v1"

// Vault stores a single encryption key in the current user's OS credential store.
type Vault interface {
	Get() (string, error)
	Set(string) error
}

var ErrKeyNotFound = errors.New("encryption key not found")

type savedConfig struct {
	Config  backup.Config `json:"config"`
	Secrets string        `json:"secrets,omitempty"`
}

type profile struct {
	Name string `json:"name"`
	savedConfig
}

type document struct {
	Version   int          `json:"version"`
	Last      *savedConfig `json:"last,omitempty"`
	LastAlias string       `json:"lastAlias,omitempty"`
	Profiles  []profile    `json:"profiles"`
}

type State struct {
	Last      *backup.Config `json:"last"`
	LastAlias string         `json:"lastAlias"`
	Aliases   []string       `json:"aliases"`
	Warning   string         `json:"warning"`
}

type Loaded struct {
	Name    string        `json:"name"`
	Config  backup.Config `json:"config"`
	Warning string        `json:"warning"`
}

type Saved struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Warning string   `json:"warning"`
}

type credentials struct {
	AccessKey    string `json:"accessKey"`
	SecretKey    string `json:"secretKey"`
	SessionToken string `json:"sessionToken"`
}

type Store struct {
	mu       sync.Mutex
	filename string
	vault    Vault
	key      []byte
	data     document
}

func Open(directory string, vault Vault) (*Store, error) {
	s := &Store{filename: filepath.Join(directory, "profiles.json"), vault: vault, data: document{Version: 1, Profiles: []profile{}}}
	f, err := os.Open(s.filename)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть настройки: %w", err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать настройки: %w", err)
	}
	if len(body) > 4*1024*1024 {
		return nil, errors.New("файл настроек слишком большой")
	}
	if err := json.Unmarshal(body, &s.data); err != nil {
		return nil, errors.New("файл профилей повреждён; существующие настройки не перезаписаны")
	}
	if s.data.Version != 1 {
		return nil, errors.New("неподдерживаемая версия файла профилей")
	}
	return s, nil
}

func (s *Store) encryptionKey(create bool) ([]byte, error) {
	if s.key != nil {
		return s.key, nil
	}
	encoded, err := s.vault.Get()
	if errors.Is(err, ErrKeyNotFound) && create {
		// Never replace a missing key if existing ciphertext still depends on it.
		if s.data.Last != nil && s.data.Last.Secrets != "" {
			return nil, errors.New("ключ шифрования сохранённых настроек отсутствует")
		}
		for _, p := range s.data.Profiles {
			if p.Secrets != "" {
				return nil, errors.New("ключ шифрования сохранённых профилей отсутствует")
			}
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := s.vault.Set(base64.StdEncoding.EncodeToString(key)); err != nil {
			return nil, err
		}
		s.key = key
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("некорректный ключ шифрования в хранилище ОС")
	}
	s.key = key
	return key, nil
}

func (s *Store) encode(c backup.Config) (savedConfig, string) {
	secret := credentials{c.AccessKey, c.SecretKey, c.SessionToken}
	c.AccessKey, c.SecretKey, c.SessionToken = "", "", ""
	entry := savedConfig{Config: c}
	if secret.AccessKey == "" && secret.SecretKey == "" && secret.SessionToken == "" {
		return entry, ""
	}
	key, err := s.encryptionKey(true)
	if err == nil {
		var block cipher.Block
		block, err = aes.NewCipher(key)
		if err == nil {
			var aead cipher.AEAD
			aead, err = cipher.NewGCM(block)
			if err == nil {
				nonce := make([]byte, aead.NonceSize())
				_, err = rand.Read(nonce)
				if err == nil {
					plain, _ := json.Marshal(secret)
					entry.Secrets = base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte(associatedData)))
					return entry, ""
				}
			}
		}
	}
	// Keyring errors may contain details from external tools. Do not echo credentials.
	return entry, "Параметры сохранены, но ключи не запомнены: защищённое хранилище ОС недоступно. Разблокируйте его и сохраните параметры повторно."
}

func (s *Store) decode(entry savedConfig) (backup.Config, string) {
	c := entry.Config
	c.AccessKey, c.SecretKey, c.SessionToken = "", "", ""
	if entry.Secrets == "" {
		return c, ""
	}
	key, err := s.encryptionKey(false)
	if err != nil {
		return c, "Параметры восстановлены, но ключи недоступны. Разблокируйте защищённое хранилище ОС и загрузите профиль повторно либо введите ключи вручную."
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return c, "Не удалось расшифровать сохранённые ключи."
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return c, "Не удалось расшифровать сохранённые ключи."
	}
	encoded, err := base64.StdEncoding.DecodeString(entry.Secrets)
	if err != nil || len(encoded) < aead.NonceSize() {
		return c, "Сохранённые ключи повреждены. Введите их повторно."
	}
	plain, err := aead.Open(nil, encoded[:aead.NonceSize()], encoded[aead.NonceSize():], []byte(associatedData))
	if err != nil {
		return c, "Сохранённые ключи повреждены или зашифрованы другим ключом. Введите их повторно."
	}
	var secret credentials
	if err := json.Unmarshal(plain, &secret); err != nil {
		return c, "Не удалось прочитать сохранённые ключи."
	}
	c.AccessKey, c.SecretKey, c.SessionToken = secret.AccessKey, secret.SecretKey, secret.SessionToken
	return c, ""
}

func (s *Store) aliases() []string {
	aliases := make([]string, 0, len(s.data.Profiles))
	for _, p := range s.data.Profiles {
		aliases = append(aliases, p.Name)
	}
	sort.Slice(aliases, func(i, j int) bool { return strings.ToLower(aliases[i]) < strings.ToLower(aliases[j]) })
	return aliases
}

func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := State{Aliases: s.aliases(), LastAlias: s.data.LastAlias}
	if s.data.Last != nil {
		c, warning := s.decode(*s.data.Last)
		state.Last, state.Warning = &c, warning
	}
	return state
}

func (s *Store) SaveLast(c backup.Config, alias string) (Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, warning := s.encode(c)
	if err := s.preserveUnavailableSecrets(s.data.Last, c, warning); err != nil {
		return Saved{}, err
	}
	next := s.data
	next.Last, next.LastAlias = &entry, alias
	if err := s.write(next); err != nil {
		return Saved{}, err
	}
	return Saved{Aliases: s.aliases(), Warning: warning}, nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return "", errors.New("укажите имя подключения: от 1 до 80 символов")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("имя подключения содержит недопустимые символы")
		}
	}
	return name, nil
}

func (s *Store) SaveProfile(name string, c backup.Config) (Saved, error) {
	name, err := validateName(name)
	if err != nil {
		return Saved{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, warning := s.encode(c)
	next := s.data
	if err := s.preserveUnavailableSecrets(s.data.Last, c, warning); err != nil {
		return Saved{}, err
	}
	next.Profiles = append([]profile(nil), s.data.Profiles...)
	index := -1
	for i, p := range next.Profiles {
		if strings.EqualFold(p.Name, name) {
			index = i
			name = p.Name
			break
		}
	}
	if index >= 0 {
		if err := s.preserveUnavailableSecrets(&next.Profiles[index].savedConfig, c, warning); err != nil {
			return Saved{}, err
		}
	}
	p := profile{Name: name, savedConfig: entry}
	if index >= 0 {
		next.Profiles[index] = p
	} else {
		next.Profiles = append(next.Profiles, p)
	}
	next.Last, next.LastAlias = &entry, name
	if err := s.write(next); err != nil {
		return Saved{}, err
	}
	return Saved{Name: name, Aliases: s.aliases(), Warning: warning}, nil
}

func (s *Store) preserveUnavailableSecrets(previous *savedConfig, c backup.Config, warning string) error {
	if previous == nil || previous.Secrets == "" {
		return nil
	}
	if warning != "" {
		return errors.New(warning + " Предыдущие сохранённые ключи оставлены без изменений.")
	}
	if c.AccessKey == "" && c.SecretKey == "" && c.SessionToken == "" {
		if _, warning := s.decode(*previous); warning != "" {
			return errors.New("Настройки не перезаписаны: сохранённые ключи недоступны. Разблокируйте защищённое хранилище ОС.")
		}
	}
	return nil
}

func (s *Store) LoadProfile(name string) (Loaded, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.data.Profiles {
		if p.Name == name {
			c, warning := s.decode(p.savedConfig)
			return Loaded{Name: p.Name, Config: c, Warning: warning}, nil
		}
	}
	return Loaded{}, errors.New("сохранённое подключение не найдено")
}

func (s *Store) DeleteProfile(name string) (Saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	next.Profiles = make([]profile, 0, len(s.data.Profiles))
	found := false
	for _, p := range s.data.Profiles {
		if p.Name == name {
			found = true
		} else {
			next.Profiles = append(next.Profiles, p)
		}
	}
	if !found {
		return Saved{}, errors.New("сохранённое подключение не найдено")
	}
	if next.LastAlias == name {
		next.LastAlias = ""
	}
	if err := s.write(next); err != nil {
		return Saved{}, err
	}
	return Saved{Aliases: s.aliases()}, nil
}

func (s *Store) write(next document) error {
	body, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if len(body) > 4*1024*1024 {
		return errors.New("слишком много сохранённых профилей")
	}
	directory := filepath.Dir(s.filename)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("не удалось создать папку настроек: %w", err)
	}
	f, err := os.CreateTemp(directory, ".profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("не удалось сохранить настройки: %w", err)
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return fmt.Errorf("не удалось записать настройки: %w", err)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, s.filename); err != nil {
		return fmt.Errorf("не удалось обновить настройки: %w", err)
	}
	s.data = next
	return nil
}
