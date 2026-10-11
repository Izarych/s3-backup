package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func revealCommand(platform, filename string) (string, []string, error) {
	switch platform {
	case "windows":
		return "explorer.exe", []string{"/select,", filename}, nil
	case "darwin":
		return "/usr/bin/open", []string{"-R", filename}, nil
	case "linux":
		return "xdg-open", []string{filepath.Dir(filename)}, nil
	default:
		return "", nil, fmt.Errorf("неподдерживаемая платформа: %s", platform)
	}
}

func RevealArchive(filename string) error {
	if filename == "" {
		return errors.New("архив ещё не создан")
	}
	if _, err := os.Stat(filename); err != nil {
		return fmt.Errorf("архив недоступен: %w", err)
	}
	program, args, err := revealCommand(runtime.GOOS, filename)
	if err != nil {
		return err
	}
	cmd := exec.Command(program, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("не удалось открыть папку архива: %w", err)
	}
	// Reap the helper process on Unix as well as Windows; do not block the GUI.
	go func() { _ = cmd.Wait() }()
	return nil
}
