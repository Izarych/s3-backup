package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Uses the real AWS CLI against a local HTTP fixture. Never sends requests to cloud storage.
func TestCLIBackupLocalS3(t *testing.T) {
	aws := os.Getenv("S3_BACKUP_TEST_AWS")
	if aws == "" {
		t.Skip("set S3_BACKUP_TEST_AWS to run the real CLI against a local fixture")
	}
	var lists, gets, denied atomic.Int32
	var unexpected atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			unexpected.Store(true)
			http.Error(w, "unexpected method", 400)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
			unexpected.Store(true)
			http.Error(w, "missing signature", 403)
			return
		}
		if r.URL.Query().Get("list-type") == "2" {
			lists.Add(1)
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>fixture</Name><IsTruncated>false</IsTruncated><KeyCount>2</KeyCount><Contents><Key>backups/denied.gz</Key><LastModified>2026-01-01T00:00:00.000Z</LastModified><ETag>&quot;denied&quot;</ETag><Size>100</Size><StorageClass>STANDARD</StorageClass></Contents><Contents><Key>-leading.txt</Key><LastModified>2026-01-01T00:00:00.000Z</LastModified><ETag>&quot;fixture&quot;</ETag><Size>5</Size><StorageClass>STANDARD</StorageClass></Contents></ListBucketResult>`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/backups/denied.gz") {
			denied.Add(1)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/-leading.txt") {
			gets.Add(1)
			if r.Header.Get("If-Match") != `"fixture"` {
				unexpected.Store(true)
				http.Error(w, "missing If-Match", 412)
				return
			}
			w.Header().Set("ETag", `"fixture"`)
			w.Header().Set("Content-Length", "5")
			fmt.Fprint(w, "hello")
			return
		}
		unexpected.Store(true)
		http.NotFound(w, r)
	}))
	defer server.Close()
	c := fixtureConfig(t.TempDir(), false)
	c.Endpoint = server.URL
	c.AWSPath = aws
	e := NewEngine()
	if err := e.Test(c); err != nil {
		t.Fatal(err)
	}
	if s := await(t, e); s.Phase != "done" {
		t.Fatalf("CLI access check failed: %+v", s)
	}
	if lists.Load() != 1 || gets.Load() != 0 || denied.Load() != 0 {
		t.Fatal("connection test downloaded an object")
	}
	if err := e.Start(c); err != nil {
		t.Fatal(err)
	}
	s := await(t, e)
	if s.Phase != "done_with_skips" || s.SkippedFiles != 1 || s.ArchivedFiles != 1 || s.DownloadedBytes != 5 {
		t.Fatalf("CLI backup failed: %+v", s)
	}
	if unexpected.Load() || lists.Load() != 2 || gets.Load() != 1 || denied.Load() != 1 {
		t.Fatalf("unexpected operations: list=%d get=%d denied=%d bad=%v", lists.Load(), gets.Load(), denied.Load(), unexpected.Load())
	}
	z, err := zip.OpenReader(s.Archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var foundObject, foundReport bool
	for _, f := range z.File {
		if f.Name == "files/-leading.txt" {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			body, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "hello" {
				t.Fatal("wrong downloaded content")
			}
			foundObject = true
		}
		if f.Name == "skipped-files.json" {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "AccessDenied") || !strings.Contains(string(body), "backups/denied.gz") {
				t.Fatal("missing CLI failure reason in report")
			}
			foundReport = true
		}
	}
	if !foundObject || !foundReport {
		t.Fatal("object or skip report not in archive")
	}
}
