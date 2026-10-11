package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"s3-backup/internal/backup"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		if err := runCheck(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := launchGUI(); err != nil {
		showError(err.Error())
		os.Exit(1)
	}
}

// This mode deliberately provides no backup operation: .env is only for checking access.
func runCheck(args []string) error {
	flags := flag.NewFlagSet("s3-backup", flag.ContinueOnError)
	envPath := flags.String("check-env", "", "Проверить подключение по .env без скачивания файлов")
	bucket := flags.String("bucket", "", "Имя бакета")
	endpoint := flags.String("endpoint", "https://storage.yandexcloud.net", "S3 endpoint")
	region := flags.String("region", "ru-central1", "Регион S3")
	awsPath := flags.String("aws", "", "Путь к AWS CLI")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *envPath == "" {
		return errors.New("используйте --check-env <путь к .env> --bucket <имя бакета>")
	}
	values, err := loadEnv(*envPath)
	if err != nil {
		return err
	}
	key, secret := values["AWS_ACCESS_KEY_ID"], values["AWS_SECRET_ACCESS_KEY"]
	if key == "" {
		key = values["YANDEX_STORAGE_KEY"]
	}
	if secret == "" {
		secret = values["YANDEX_STORAGE_SECRET"]
	}
	if *bucket == "" {
		*bucket = values["AWS_BUCKET"]
	}
	c := backup.Config{AccessKey: key, SecretKey: secret, SessionToken: values["AWS_SESSION_TOKEN"], Endpoint: *endpoint, Region: *region, Bucket: *bucket, AWSPath: *awsPath}
	source, err := backup.NewCLI(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	page, err := source.List(ctx, "", 1)
	if err != nil {
		return err
	}
	fmt.Printf("Подключение успешно: бакет %s, endpoint %s. ListObjectsV2 вернул %d объект(ов). Файлы не скачивались.\n", c.Bucket, c.Endpoint, len(page.Contents))
	return nil
}

func loadEnv(filename string) (map[string]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть .env: %w", err)
	}
	defer f.Close()
	values := make(map[string]string)
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(s.Text(), "\ufeff"))
		line = strings.TrimPrefix(line, "export ")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		values[strings.TrimSpace(key)] = value
	}
	return values, s.Err()
}
