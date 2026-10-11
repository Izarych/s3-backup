package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// createMacDMG packages a signed .app as a compressed, read-only disk image.
// It is called by the native macOS build and uses tools included with macOS.
func createMacDMG(bundle, arch string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("DMG packaging requires macOS and hdiutil")
	}
	directory := filepath.Dir(bundle)
	work, err := os.MkdirTemp(directory, ".dmg-")
	if err != nil {
		return "", fmt.Errorf("creating DMG staging directory: %w", err)
	}
	defer os.RemoveAll(work)
	root := filepath.Join(work, "volume")
	if err := os.Mkdir(root, 0755); err != nil {
		return "", err
	}
	// ditto preserves bundle permissions, resource forks, symlinks and signatures.
	copy := exec.Command("ditto", bundle, filepath.Join(root, "S3 Backup.app"))
	copy.Stdout, copy.Stderr = os.Stdout, os.Stderr
	if err := copy.Run(); err != nil {
		return "", fmt.Errorf("copying app into DMG: %w", err)
	}
	if err := os.Symlink("/Applications", filepath.Join(root, "Applications")); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "Установка.txt"), []byte(macInstallInstructions), 0644); err != nil {
		return "", err
	}
	partial := filepath.Join(work, "image.dmg")
	create := exec.Command("hdiutil", "create", "-volname", "S3 Backup", "-srcfolder", root, "-fs", "HFS+", "-format", "UDZO", partial)
	create.Stdout, create.Stderr = os.Stdout, os.Stderr
	if err := create.Run(); err != nil {
		return "", fmt.Errorf("creating DMG failed: %w", err)
	}
	verify := exec.Command("hdiutil", "verify", partial)
	verify.Stdout, verify.Stderr = os.Stdout, os.Stderr
	if err := verify.Run(); err != nil {
		return "", fmt.Errorf("verifying DMG failed: %w", err)
	}
	final := filepath.Join(directory, "s3-backup-macos-"+arch+".dmg")
	if err := os.Rename(partial, final); err != nil {
		return "", fmt.Errorf("saving DMG failed: %w", err)
	}
	return final, nil
}

const macInstallInstructions = `S3 Backup — установка на macOS

1. Перетащите S3 Backup.app на ссылку Applications в этом окне.
2. Извлеките диск S3 Backup и запустите приложение из Applications.
3. Для работы с хранилищем установите AWS CLI v2 для macOS:
   https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html
   При наличии Homebrew: brew install awscli
4. Введите credentials, endpoint, регион и бакет. Выберите папку для архива.
5. Для начала используйте «Проверить подключение»: эта кнопка не скачивает файлы.

amd64 — Mac с процессором Intel.
arm64 — Mac с Apple Silicon (M1/M2/M3/M4 и новее).

Эта тестовая сборка имеет ad-hoc подпись, без Apple notarization.
Если macOS блокирует запуск загруженного приложения, после попытки открытия
используйте «Системные настройки → Конфиденциальность и безопасность → Всё равно открыть».

Профили сохраняются в ~/Library/Application Support/S3Backup.
Ключи подключения шифруются; ключ шифрования хранится в macOS Keychain.
`
