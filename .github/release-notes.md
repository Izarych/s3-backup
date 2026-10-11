## Скачать

| Система | Файл |
|---|---|
| Windows 10/11, x64 | **s3-backup-windows-amd64.exe** |
| macOS, Intel | **s3-backup-macos-amd64.dmg** |
| macOS, Apple Silicon (M-серия) | **s3-backup-macos-arm64.dmg** |
| Linux, x64 | **s3-backup-linux-amd64.tar.gz** |

## Возможности

- Бэкап S3 / Yandex Object Storage в ZIP64 через AWS CLI.
- Прогресс, скорость скачивания и статусы файлов.
- Продолжение загрузки при ошибках отдельных объектов; отчёт о пропусках внутри ZIP.
- Автосохранение параметров и именованные подключения.
- Шифрование credentials с ключом в защищённом хранилище ОС.

## Установка

**На всех системах нужен [AWS CLI v2](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html).** Файлы релиза содержат само приложение; AWS CLI устанавливается отдельно.

- **Windows:** скачайте и запустите `.exe`. Требуется [WebView2 Runtime](https://developer.microsoft.com/microsoft-edge/webview2/), обычно уже установленный в Windows.
- **macOS:** откройте `.dmg`, перетащите приложение в Applications и запускайте оттуда. AWS CLI можно установить командой `brew install awscli`. Сборка имеет ad-hoc подпись без Apple notarization; при блокировке первого запуска после попытки открытия используйте **«Системные настройки → Конфиденциальность и безопасность → Всё равно открыть»**.
- **Linux:** распакуйте архив и запустите `./s3-backup` в графической сессии. Сборка для Ubuntu 22.04+ и совместимых систем; нужны GTK 3, WebKitGTK 4.1 и `xdg-utils`. Для Debian/Ubuntu: `sudo apt-get install libgtk-3-0 libwebkit2gtk-4.1-0 xdg-utils`. Для запоминания ключей нужен доступный Secret Service, например GNOME Keyring.

Введите параметры подключения и выберите папку. **«Проверить подключение»** проверяет доступ к списку объектов без скачивания файлов. **«Создать бэкап»** запускает загрузку и упаковку в архив.

Контрольные суммы файлов — в `SHA256SUMS`.
