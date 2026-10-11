package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"s3-backup/internal/backup"
	"s3-backup/internal/desktop"
	"s3-backup/internal/profiles"
)

//go:embed ui/*
var assets embed.FS

// App exposes the same GUI actions on all three desktop platforms.
type App struct {
	engine      *backup.Engine
	mu          sync.RWMutex
	ctx         context.Context
	profiles    *profiles.Store
	profilesErr error
	uiReady     bool
	closing     bool
}

func newApp() *App {
	app := &App{engine: backup.NewEngine()}
	directory, err := os.UserConfigDir()
	if err == nil {
		app.profiles, err = profiles.Open(filepath.Join(directory, "S3Backup"), profiles.SystemVault{})
	}
	app.profilesErr = err
	return app
}

func (a *App) GetSavedSettings() (profiles.State, error) {
	if a.profilesErr != nil {
		return profiles.State{}, a.profilesErr
	}
	return a.profiles.State(), nil
}

func (a *App) SaveLastSettings(c backup.Config, alias string) (profiles.Saved, error) {
	if a.profilesErr != nil {
		return profiles.Saved{}, a.profilesErr
	}
	return a.profiles.SaveLast(c, alias)
}

func (a *App) SaveConnection(name string, c backup.Config) (profiles.Saved, error) {
	if a.profilesErr != nil {
		return profiles.Saved{}, a.profilesErr
	}
	return a.profiles.SaveProfile(name, c)
}

func (a *App) LoadConnection(name string) (profiles.Loaded, error) {
	if a.profilesErr != nil {
		return profiles.Loaded{}, a.profilesErr
	}
	return a.profiles.LoadProfile(name)
}

func (a *App) DeleteConnection(name string) (profiles.Saved, error) {
	if a.profilesErr != nil {
		return profiles.Saved{}, a.profilesErr
	}
	return a.profiles.DeleteProfile(name)
}

func (a *App) FrontendReady() {
	a.mu.Lock()
	a.uiReady = true
	a.mu.Unlock()
}

func (a *App) CloseAfterSaving() {
	a.mu.Lock()
	a.closing = true
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wailsruntime.Quit(ctx)
	}
}

func (a *App) beforeClose(ctx context.Context) bool {
	a.mu.RLock()
	ready, closing := a.uiReady, a.closing
	a.mu.RUnlock()
	if !ready || closing {
		return false
	}
	wailsruntime.EventsEmit(ctx, "save-before-close")
	return true
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

func (a *App) GetState() backup.State                { return a.engine.Snapshot() }
func (a *App) CheckConnection(c backup.Config) error { return a.engine.Test(c) }
func (a *App) StartBackup(c backup.Config) error     { return a.engine.Start(c) }
func (a *App) CancelBackup()                         { a.engine.Cancel() }

func (a *App) ChooseDirectory() (string, error) {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx == nil {
		return "", fmt.Errorf("окно приложения ещё не готово")
	}
	return wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: "Выберите папку для резервной копии",
	})
}

func (a *App) OpenArchiveFolder() error { return desktop.RevealArchive(a.engine.Snapshot().Archive) }

func (a *App) GetEnvironment() map[string]string {
	aws, err := backup.FindAWS("")
	if err != nil {
		return map[string]string{"aws": "", "message": err.Error(), "version": version}
	}
	return map[string]string{"aws": aws, "message": "AWS CLI готов", "version": version}
}

func launchGUI() error {
	frontend, err := fs.Sub(assets, "ui")
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	app := newApp()
	defer app.engine.Shutdown()
	return wails.Run(&options.App{
		Title: "S3 Backup", Width: 1000, Height: 950, MinWidth: 800, MinHeight: 700,
		BackgroundColour: options.NewRGB(245, 246, 250),
		AssetServer:      &assetserver.Options{Assets: frontend},
		OnStartup:        app.startup,
		OnBeforeClose:    app.beforeClose,
		Bind:             []interface{}{app},
		Windows: &windows.Options{
			Theme:               windows.Light,
			WebviewUserDataPath: filepath.Join(cache, "S3Backup", "wails"),
		},
	})
}
