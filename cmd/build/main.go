// Build and package the native desktop app on Windows, Linux or macOS.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

func main() {
	arch := flag.String("arch", runtime.GOARCH, "Architecture: amd64 or arm64")
	version := flag.String("version", "1.0.0", "Release version without the v prefix")
	flag.Parse()
	if err := build(*arch, *version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(arch, version string) error {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`).MatchString(version) {
		return fmt.Errorf("invalid release version: %s", version)
	}
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported architecture: %s", arch)
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	if arch != runtime.GOARCH && runtime.GOOS != "darwin" {
		return fmt.Errorf("build %s/%s on a native runner of that architecture", runtime.GOOS, arch)
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run this command from the project root: %w", err)
	}
	output := filepath.Join("dist", runtime.GOOS+"-"+arch, "s3-backup")
	tags := "production"
	ldflags := "-s -w -X main.version=" + version
	switch runtime.GOOS {
	case "windows":
		// Keep the existing portable Windows distribution and bundled AWS CLI.
		output = filepath.Join("dist", "s3-backup.exe")
		ldflags = "-H=windowsgui " + ldflags
	case "linux":
		tags += ",webkit2_41"
	case "darwin":
		output = filepath.Join("dist", "darwin-"+arch, "S3 Backup.app", "Contents", "MacOS", "s3-backup")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-tags", tags, "-ldflags", ldflags, "-o", output, ".")
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GOARCH=") && !strings.HasPrefix(v, "GOOS=") && !strings.HasPrefix(v, "CGO_ENABLED=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cgo := "1"
	if runtime.GOOS == "windows" {
		cgo = "0"
	}
	cmd.Env = append(cmd.Env, "GOARCH="+arch, "GOOS="+runtime.GOOS, "CGO_ENABLED="+cgo)
	if runtime.GOOS == "darwin" && os.Getenv("MACOSX_DEPLOYMENT_TARGET") == "" {
		cmd.Env = append(cmd.Env, "MACOSX_DEPLOYMENT_TARGET=11.0")
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("native build failed: %w", err)
	}
	if runtime.GOOS == "darwin" {
		contents := filepath.Dir(filepath.Dir(output))
		if err := os.MkdirAll(filepath.Join(contents, "Resources"), 0755); err != nil {
			return err
		}
		plist := strings.ReplaceAll(macPlist, "{{VERSION}}", strings.SplitN(version, "-", 2)[0])
		if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(contents, "PkgInfo"), []byte("APPL????"), 0644); err != nil {
			return err
		}
		bundle := filepath.Dir(contents)
		// Ad-hoc signing makes the bundle runnable locally, including on Apple Silicon.
		sign := exec.Command("codesign", "--force", "--deep", "--sign", "-", bundle)
		sign.Stdout, sign.Stderr = os.Stdout, os.Stderr
		if err := sign.Run(); err != nil {
			return fmt.Errorf("signing .app failed: %w", err)
		}
		verify := exec.Command("codesign", "--verify", "--deep", "--strict", bundle)
		verify.Stdout, verify.Stderr = os.Stdout, os.Stderr
		if err := verify.Run(); err != nil {
			return fmt.Errorf("verifying .app signature failed: %w", err)
		}
		fmt.Println("Built:", bundle)
		image, err := createMacDMG(bundle, arch)
		if err != nil {
			return err
		}
		fmt.Println("DMG:", image)
	} else {
		fmt.Println("Built:", output)
	}
	return nil
}

const macPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>S3 Backup</string>
<key>CFBundleDisplayName</key><string>S3 Backup</string>
<key>CFBundleIdentifier</key><string>io.s3backup.desktop</string>
<key>CFBundleExecutable</key><string>s3-backup</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>{{VERSION}}</string>
<key>CFBundleVersion</key><string>{{VERSION}}</string>
<key>LSMinimumSystemVersion</key><string>11.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSRequiresAquaSystemAppearance</key><true/>
</dict></plist>
`
