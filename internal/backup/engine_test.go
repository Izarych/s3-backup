package backup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureSource struct {
	objects  []Object
	data     map[string]string
	failures map[string]error
	block    bool
	started  chan struct{}
	once     sync.Once
}

func (s *fixtureSource) List(ctx context.Context, token string, limit int) (Page, error) {
	if token == "" && len(s.objects) > 2 {
		return Page{Contents: s.objects[:2], IsTruncated: true, NextContinuationToken: "page2"}, nil
	}
	if token == "page2" {
		return Page{Contents: s.objects[2:]}, nil
	}
	return Page{Contents: s.objects}, nil
}

func (s *fixtureSource) Download(ctx context.Context, obj Object, dest string) error {
	if failure := s.failures[obj.Key]; failure != nil {
		if err := os.WriteFile(dest, []byte("partial download"), 0600); err != nil {
			return err
		}
		return failure
	}
	if s.block {
		if err := os.WriteFile(dest, []byte("partial"), 0600); err != nil {
			return err
		}
		s.once.Do(func() { close(s.started) })
		<-ctx.Done()
		return ctx.Err()
	}
	return os.WriteFile(dest, []byte(s.data[obj.Key]), 0600)
}

func fixtureConfig(dir string, compress bool) Config {
	return Config{Bucket: "fixture", Region: "ru-central1", AccessKey: "test", SecretKey: "test", Destination: dir, Parallel: 4, Compress: compress}
}

func await(t *testing.T, e *Engine) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s := e.Snapshot()
		if !s.Busy {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("engine did not finish")
	return State{}
}

func TestBackupRoundTrip(t *testing.T) {
	for _, compress := range []bool{false, true} {
		t.Run(map[bool]string{false: "store", true: "deflate"}[compress], func(t *testing.T) {
			dir := t.TempDir()
			data := map[string]string{
				"images/фото.txt": "hello world",
				"empty/":          "",
				"../outside.txt":  "traversal",
				"Images/ФОТО.txt": "case-sensitive",
				"a":               "file conflicts with folder",
				"a/child.txt":     "child",
				"data/":           "non-empty slash key",
				"CON":             "reserved name",
			}
			source := &fixtureSource{data: data}
			for key, body := range data {
				source.objects = append(source.objects, Object{Key: key, Size: int64(len(body)), ETag: "fixture-etag", LastModified: time.Now().UTC()})
			}
			e := NewEngine()
			if err := e.StartSource(fixtureConfig(dir, compress), source); err != nil {
				t.Fatal(err)
			}
			s := await(t, e)
			if s.Phase != "done" || s.ArchivedFiles != len(data) || s.DownloadedFiles != len(data) || s.Progress != 100 {
				t.Fatalf("unexpected state: %+v", s)
			}
			z, err := zip.OpenReader(s.Archive)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			entries := make(map[string]*zip.File)
			folded := make(map[string]bool)
			for _, f := range z.File {
				if strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "../") {
					t.Fatalf("unsafe archive path %q", f.Name)
				}
				name := strings.ToLower(strings.TrimSuffix(f.Name, "/"))
				if folded[name] {
					t.Fatalf("case collision: %s", f.Name)
				}
				folded[name] = true
				entries[f.Name] = f
			}
			mf := entries["backup-manifest.json"]
			if mf == nil {
				t.Fatal("no manifest")
			}
			r, err := mf.Open()
			if err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Objects []manifestEntry `json:"objects"`
			}
			if err := json.NewDecoder(r).Decode(&manifest); err != nil {
				t.Fatal(err)
			}
			r.Close()
			if len(manifest.Objects) != len(data) {
				t.Fatal("incomplete manifest")
			}
			for _, obj := range manifest.Objects {
				f := entries[obj.ArchivePath]
				if f == nil {
					t.Fatalf("missing %q", obj.Key)
				}
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
				if string(body) != data[obj.Key] {
					t.Fatalf("content mismatch for %q", obj.Key)
				}
				if !strings.HasSuffix(obj.ArchivePath, "/") {
					for other := range entries {
						if strings.HasPrefix(strings.ToLower(other), strings.ToLower(obj.ArchivePath)+"/") {
							t.Fatalf("file/directory conflict: %s", obj.ArchivePath)
						}
					}
				}
			}
			files, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0].Name() != filepath.Base(s.Archive) {
				t.Fatalf("temporary files left behind: %v", files)
			}
		})
	}
}

func TestCancellationCleansPartialAndAllowsRestart(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine()
	source := &fixtureSource{objects: []Object{{Key: "large.bin", Size: 100}}, block: true, started: make(chan struct{})}
	if err := e.StartSource(fixtureConfig(dir, false), source); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.started:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	if err := e.StartSource(fixtureConfig(dir, false), source); err == nil {
		t.Fatal("allowed overlapping backups")
	}
	e.Cancel()
	s := await(t, e)
	if s.Phase != "cancelled" || s.Archive != "" {
		t.Fatalf("unexpected state: %+v", s)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("partial files survived cancellation: %v", files)
	}
	if err := e.StartSource(fixtureConfig(dir, false), &fixtureSource{}); err != nil {
		t.Fatal(err)
	}
	if s := await(t, e); s.Phase != "done" {
		t.Fatalf("restart failed: %+v", s)
	}
}

func TestChangedObjectIsSkipped(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine()
	source := &fixtureSource{objects: []Object{{Key: "changed", Size: 100}}, data: map[string]string{"changed": "short"}}
	if err := e.StartSource(fixtureConfig(dir, false), source); err != nil {
		t.Fatal(err)
	}
	s := await(t, e)
	if s.Phase != "done_with_skips" || s.Archive == "" || s.SkippedFiles != 1 || s.DownloadedFiles != 0 || s.ArchivedFiles != 0 || s.SkippedBytes != 100 {
		t.Fatalf("unexpected state: %+v", s)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != filepath.Base(s.Archive) {
		t.Fatal("temporary files remained after skip")
	}
	z, err := zip.OpenReader(s.Archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 2 {
		t.Fatal("expected manifest and skip report, without corrupt object entry")
	}
	for _, f := range z.File {
		if f.Name != "backup-manifest.json" && f.Name != "skipped-files.json" {
			t.Fatalf("unexpected entry %q", f.Name)
		}
	}
}

func TestDownloadFailuresContinueAndReport(t *testing.T) {
	for _, parallel := range []int{1, 4} {
		for _, allDenied := range []bool{false, true} {
			t.Run(fmt.Sprintf("parallel=%d/allDenied=%v", parallel, allDenied), func(t *testing.T) {
				dir := t.TempDir()
				source := &fixtureSource{
					objects:  []Object{{Key: "backups/denied.gz", Size: 400}, {Key: "deleted.txt", Size: 50}, {Key: "images/ok.txt", Size: 5}},
					data:     map[string]string{"images/ok.txt": "hello"},
					failures: map[string]error{"backups/denied.gz": errors.New("AccessDenied: Access Denied"), "deleted.txt": errors.New("NoSuchKey")},
				}
				wantSkipped, wantArchived := 2, 1
				var wantBytes int64 = 5
				if allDenied {
					source.failures["images/ok.txt"] = errors.New("AccessDenied")
					wantSkipped, wantArchived, wantBytes = 3, 0, 0
				}
				c := fixtureConfig(dir, true)
				c.Parallel = parallel
				e := NewEngine()
				if err := e.StartSource(c, source); err != nil {
					t.Fatal(err)
				}
				s := await(t, e)
				if s.Phase != "done_with_skips" || s.SkippedFiles != wantSkipped || s.ArchivedFiles != wantArchived || s.DownloadedFiles != wantArchived || s.DownloadedBytes != wantBytes || s.ArchivedBytes != wantBytes || s.Progress != 100 || s.SkippedBytes != s.TotalBytes-wantBytes {
					t.Fatalf("unexpected state: %+v", s)
				}
				if !strings.Contains(s.Message, "skipped-files.json") {
					t.Fatal("no report location in result")
				}
				for _, entry := range s.Entries {
					if entry.Status == "skipped" && (entry.Error == "" || entry.Bytes != 0) {
						t.Fatalf("incorrect skip entry: %+v", entry)
					}
				}
				z, err := zip.OpenReader(s.Archive)
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				var manifest struct {
					Format        int             `json:"format"`
					Complete      bool            `json:"complete"`
					ArchivedFiles int             `json:"archivedFiles"`
					SkippedFiles  int             `json:"skippedFiles"`
					Objects       []manifestEntry `json:"objects"`
				}
				var skipped []manifestEntry
				var dataFiles int
				for _, f := range z.File {
					r, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					switch f.Name {
					case "backup-manifest.json":
						err = json.NewDecoder(r).Decode(&manifest)
					case "skipped-files.json":
						err = json.NewDecoder(r).Decode(&skipped)
					case "files/images/ok.txt":
						var body []byte
						body, err = io.ReadAll(r)
						if string(body) != "hello" {
							t.Fatal("wrong successful object")
						}
						dataFiles++
					default:
						t.Fatalf("failed object added to ZIP: %q", f.Name)
					}
					r.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
				if dataFiles != wantArchived || len(skipped) != wantSkipped || manifest.Format != 2 || manifest.Complete || manifest.ArchivedFiles != wantArchived || manifest.SkippedFiles != wantSkipped || len(manifest.Objects) != 3 {
					t.Fatalf("incorrect manifest/report: %+v; skipped=%+v", manifest, skipped)
				}
				for _, obj := range manifest.Objects {
					if failure := source.failures[obj.Key]; failure != nil {
						if obj.Status != "skipped" || obj.ArchivePath != "" || obj.Error != failure.Error() {
							t.Fatalf("incorrect skipped manifest entry: %+v", obj)
						}
					} else if obj.Status != "archived" || obj.ArchivePath == "" || obj.Error != "" {
						t.Fatalf("incorrect successful manifest entry: %+v", obj)
					}
				}
				for _, obj := range skipped {
					if obj.Status != "skipped" || obj.Error != source.failures[obj.Key].Error() {
						t.Fatalf("incorrect skip report entry: %+v", obj)
					}
				}
				files, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(files) != 1 {
					t.Fatalf("partial downloads remained: %v", files)
				}
			})
		}
	}
}

type brokenPagination struct{}

func (brokenPagination) List(context.Context, string, int) (Page, error) {
	return Page{IsTruncated: true}, nil
}
func (brokenPagination) Download(context.Context, Object, string) error {
	return errors.New("should not download")
}

func TestBrokenPaginationDoesNotDownload(t *testing.T) {
	e := NewEngine()
	if err := e.StartSource(fixtureConfig(t.TempDir(), false), brokenPagination{}); err != nil {
		t.Fatal(err)
	}
	if s := await(t, e); s.Phase != "error" {
		t.Fatalf("unexpected state: %+v", s)
	}
}

type slowSource struct{}

func (slowSource) List(context.Context, string, int) (Page, error) {
	return Page{Contents: []Object{{Key: "folder/slow.bin", Size: 4096}}}, nil
}

func (slowSource) Download(ctx context.Context, obj Object, target string) error {
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	for i := 0; i < 8; i++ {
		if _, err := f.Write(make([]byte, 512)); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}

func TestLiveDownloadProgressAndSpeed(t *testing.T) {
	e := NewEngine()
	if err := e.StartSource(fixtureConfig(t.TempDir(), false), slowSource{}); err != nil {
		t.Fatal(err)
	}
	var sawProgress, sawSpeed bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := e.Snapshot()
		if !s.Busy {
			if s.Phase != "done" {
				t.Fatalf("failed: %+v", s)
			}
			break
		}
		if s.DownloadedBytes > 0 && s.DownloadedBytes < s.TotalBytes && len(s.Entries) > 0 && s.Entries[0].Status == "downloading" {
			sawProgress = true
			if s.Progress <= 0 || s.Progress >= 100 {
				t.Fatalf("invalid progress: %f", s.Progress)
			}
		}
		if s.Speed > 0 {
			sawSpeed = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e.Snapshot().Busy {
		e.Cancel()
		e.Shutdown()
		t.Fatal("download did not finish")
	}
	if !sawProgress || !sawSpeed {
		t.Fatalf("missing live reporting: progress=%v speed=%v", sawProgress, sawSpeed)
	}
}
