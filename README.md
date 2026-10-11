# S3 Backup

Десктопное приложение на Go для резервного копирования S3 / Yandex Object Storage в ZIP64. Windows, macOS и Linux.

**[Скачать готовую версию](https://github.com/Izarych/s3-backup/releases/latest)**

Инструкции по установке, требования и описание возможностей находятся в релизе.

Сборка из исходников: `./build.ps1` на Windows или `bash ./build.sh` на Linux/macOS. Нужен Go 1.26+ и нативные зависимости Wails.

Публикация новой версии: push тега `vX.Y.Z`. GitHub Actions проверит сборки и опубликует готовые файлы в Releases.
