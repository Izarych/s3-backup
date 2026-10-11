package profiles

import (
	"errors"

	"github.com/zalando/go-keyring"
)

type SystemVault struct{ service string }

func (v SystemVault) name() string {
	if v.service != "" {
		return v.service
	}
	return service
}

func (v SystemVault) Get() (string, error) {
	key, err := keyring.Get(v.name(), keyAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrKeyNotFound
	}
	return key, err
}

func (v SystemVault) Set(key string) error { return keyring.Set(v.name(), keyAccount, key) }
