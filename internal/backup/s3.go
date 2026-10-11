package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Config struct {
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	AccessKey    string `json:"accessKey"`
	SecretKey    string `json:"secretKey"`
	SessionToken string `json:"sessionToken"`
	Prefix       string `json:"prefix"`
	Destination  string `json:"destination"`
	Parallel     int    `json:"parallel"`
	Compress     bool   `json:"compress"`
	AWSPath      string `json:"awsPath"`
}

func (c *Config) Validate(destination bool) error {
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Region = strings.TrimSpace(c.Region)
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.AccessKey = strings.TrimSpace(c.AccessKey)
	c.SecretKey = strings.TrimSpace(c.SecretKey)
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("укажите корректный endpoint: https://storage.yandexcloud.net")
		}
	}
	if c.Region == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return errors.New("заполните регион, бакет, Access Key ID и Secret Access Key")
	}
	if strings.ContainsAny(c.Bucket, "/\\\x00\r\n") || strings.HasPrefix(c.Bucket, "-") {
		return errors.New("в поле бакета нужно только его имя, без s3:// и пути")
	}
	if c.Parallel == 0 {
		c.Parallel = 4
	}
	if c.Parallel < 1 || c.Parallel > 8 {
		return errors.New("число параллельных загрузок должно быть от 1 до 8")
	}
	if destination {
		if c.Destination == "" {
			return errors.New("выберите папку для архива")
		}
		info, err := os.Stat(c.Destination)
		if err != nil || !info.IsDir() {
			return errors.New("папка для архива не существует")
		}
	}
	return nil
}

func FindAWS(custom string) (string, error) {
	if strings.TrimSpace(custom) != "" {
		p, err := exec.LookPath(strings.TrimSpace(custom))
		if err == nil {
			return p, nil
		}
		return "", errors.New("AWS CLI не найден по указанному пути")
	}
	var executable string
	if p, err := os.Executable(); err == nil {
		executable = p
	}
	home, _ := os.UserHomeDir()
	for _, p := range awsCandidates(runtime.GOOS, executable, home, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")) {
		if p, err := exec.LookPath(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("aws"); err == nil {
		return p, nil
	}
	return "", errors.New("AWS CLI не установлен. Установите AWS CLI v2 и перезапустите приложение или укажите путь к исполняемому файлу AWS CLI в дополнительных настройках")
}

func awsCandidates(platform, executable, home, programFiles, programFilesX86 string) []string {
	name := "aws"
	if platform == "windows" {
		name = "aws.exe"
	}
	var candidates []string
	if executable != "" {
		base := filepath.Dir(executable)
		candidates = append(candidates, filepath.Join(base, "aws-cli", name))
		if platform == "darwin" {
			// Bundled CLI inside .app/Contents/Resources or beside the .app bundle.
			candidates = append(candidates, filepath.Join(base, "..", "Resources", "aws-cli", name), filepath.Join(base, "..", "..", "..", "aws-cli", name))
		}
	}
	if platform == "windows" {
		for _, base := range []string{programFiles, programFilesX86} {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, "Amazon", "AWSCLIV2", name))
			}
		}
	} else {
		// Finder/desktop launchers often provide a much shorter PATH than a shell.
		candidates = append(candidates, "/usr/local/bin/aws", "/usr/bin/aws", "/opt/homebrew/bin/aws", "/usr/local/aws-cli/v2/current/bin/aws")
		if home != "" {
			candidates = append(candidates, filepath.Join(home, ".local", "bin", "aws"), filepath.Join(home, "bin", "aws"))
		}
	}
	return candidates
}

type Object struct {
	Key          string    `json:"Key"`
	Size         int64     `json:"Size"`
	ETag         string    `json:"ETag"`
	LastModified time.Time `json:"LastModified"`
}

type Page struct {
	Contents              []Object `json:"Contents"`
	IsTruncated           bool     `json:"IsTruncated"`
	NextContinuationToken string   `json:"NextContinuationToken"`
}

type Source interface {
	List(context.Context, string, int) (Page, error)
	Download(context.Context, Object, string) error
}

type CLI struct {
	config     Config
	executable string
}

func NewCLI(c Config) (*CLI, error) {
	if err := c.Validate(false); err != nil {
		return nil, err
	}
	p, err := FindAWS(c.AWSPath)
	if err != nil {
		return nil, err
	}
	return &CLI{config: c, executable: p}, nil
}

func (s *CLI) command(ctx context.Context, args ...string) ([]byte, error) {
	global := []string{"--region", s.config.Region, "--output", "json", "--no-cli-pager", "--cli-connect-timeout", "15", "--cli-read-timeout", "60"}
	if s.config.Endpoint != "" {
		global = append(global, "--endpoint-url", s.config.Endpoint)
	}
	cmd := exec.CommandContext(ctx, s.executable, append(global, args...)...)
	hideWindow(cmd)
	// Do not inherit an AWS profile, role, custom CA or credentials from the host.
	for _, v := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(v), "AWS_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env,
		"AWS_ACCESS_KEY_ID="+s.config.AccessKey,
		"AWS_SECRET_ACCESS_KEY="+s.config.SecretKey,
		"AWS_SESSION_TOKEN="+s.config.SessionToken,
		"AWS_DEFAULT_REGION="+s.config.Region,
		"AWS_CONFIG_FILE="+os.DevNull,
		"AWS_SHARED_CREDENTIALS_FILE="+os.DevNull,
		"AWS_EC2_METADATA_DISABLED=true", "AWS_PAGER=", "AWS_CLI_AUTO_PROMPT=off",
		"AWS_RETRY_MODE=standard", "AWS_MAX_ATTEMPTS=5")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// Never return credentials to logs or the interface, even if CLI echoed them.
		for _, value := range []string{s.config.AccessKey, s.config.SecretKey, s.config.SessionToken} {
			if value != "" {
				msg = strings.ReplaceAll(msg, value, "[hidden]")
			}
		}
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		return nil, fmt.Errorf("AWS CLI: %s", msg)
	}
	return out, nil
}

func (s *CLI) List(ctx context.Context, token string, limit int) (Page, error) {
	args := []string{"s3api", "list-objects-v2", "--bucket", s.config.Bucket, "--max-keys", fmt.Sprint(limit), "--no-paginate"}
	if s.config.Prefix != "" {
		args = append(args, "--prefix="+s.config.Prefix)
	}
	if token != "" {
		args = append(args, "--continuation-token="+token)
	}
	out, err := s.command(ctx, args...)
	if err != nil {
		return Page{}, err
	}
	var p Page
	if err := json.Unmarshal(out, &p); err != nil {
		return Page{}, fmt.Errorf("не удалось разобрать ответ S3: %w", err)
	}
	return p, nil
}

func (s *CLI) Download(ctx context.Context, obj Object, target string) error {
	args := []string{"s3api", "get-object", "--bucket", s.config.Bucket, "--key=" + obj.Key}
	// Refuse silently changed objects between inventory and download.
	if obj.ETag != "" {
		args = append(args, "--if-match="+obj.ETag)
	}
	args = append(args, target)
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = s.command(ctx, args...)
		if err == nil || ctx.Err() != nil {
			return err
		}
		// Streaming errors can happen after the SDK's request-level retry logic.
		message := err.Error()
		permanent := []string{"AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "NoSuchKey", "NoSuchBucket", "PreconditionFailed", "InvalidObjectState", "disk full", "No space left", "Permission denied"}
		for _, code := range permanent {
			if strings.Contains(message, code) {
				return err
			}
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}
	return err
}
