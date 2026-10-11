//go:build !windows && !linux && !darwin

package backup

import "errors"

func availableSpace(string) (uint64, error) {
	return 0, errors.New("проверка свободного места не поддерживается на этой платформе")
}
