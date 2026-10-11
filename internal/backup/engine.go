package backup

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Entry struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Size   int64  `json:"size"`
	Bytes  int64  `json:"bytes"`
	Error  string `json:"error,omitempty"`
}

type State struct {
	Phase           string  `json:"phase"`
	Message         string  `json:"message"`
	Busy            bool    `json:"busy"`
	TotalFiles      int     `json:"totalFiles"`
	DownloadedFiles int     `json:"downloadedFiles"`
	ArchivedFiles   int     `json:"archivedFiles"`
	SkippedFiles    int     `json:"skippedFiles"`
	TotalBytes      int64   `json:"totalBytes"`
	DownloadedBytes int64   `json:"downloadedBytes"`
	ArchivedBytes   int64   `json:"archivedBytes"`
	SkippedBytes    int64   `json:"skippedBytes"`
	Speed           float64 `json:"speed"`
	Progress        float64 `json:"progress"`
	Elapsed         float64 `json:"elapsed"`
	ETA             float64 `json:"eta"`
	Archive         string  `json:"archive"`
	Entries         []Entry `json:"entries"`
}

type active struct {
	object Object
	file   string
}

type Engine struct {
	mu         sync.Mutex
	state      State
	active     map[int]active
	history    []Entry
	cancel     context.CancelFunc
	done       chan struct{}
	started    time.Time
	lastSample time.Time
	lastBytes  int64
}

func NewEngine() *Engine {
	return &Engine{state: State{Phase: "idle", Message: "Готов к подключению"}, active: make(map[int]active)}
}

func (e *Engine) begin(phase, message string) (context.Context, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Busy {
		return nil, errors.New("дождитесь завершения текущей операции")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.done = make(chan struct{})
	e.started = time.Now()
	e.lastSample = e.started
	e.lastBytes = 0
	e.history = nil
	e.active = make(map[int]active)
	e.state = State{Phase: phase, Message: message, Busy: true}
	return ctx, nil
}

func (e *Engine) finish(err error, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state.Busy = false
	e.state.Speed = 0
	e.state.Elapsed = time.Since(e.started).Seconds()
	e.state.Message = message
	if errors.Is(err, context.Canceled) {
		e.state.Phase = "cancelled"
		e.state.Message = "Операция отменена. Незавершённый архив и временные файлы удалены."
	} else if err != nil {
		e.state.Phase = "error"
		e.state.Message = err.Error()
	} else {
		e.state.Phase = "done"
		if e.state.Archive != "" && e.state.SkippedFiles > 0 {
			e.state.Phase = "done_with_skips"
			e.state.Message = fmt.Sprintf("Обработка завершена. В архиве: %d из %d файлов; пропущено: %d. Причины — в skipped-files.json внутри архива.", e.state.ArchivedFiles, e.state.TotalFiles, e.state.SkippedFiles)
		}
		e.state.Progress = 100
	}
	e.active = make(map[int]active)
	if e.cancel != nil {
		e.cancel()
	}
	e.cancel = nil
	close(e.done)
}

func (e *Engine) Cancel() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.state.Message = "Отмена… Завершаем процессы и очищаем временные файлы."
		e.cancel()
	}
}

func (e *Engine) Shutdown() {
	e.mu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	done := e.done
	e.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (e *Engine) Snapshot() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.state
	s.Entries = make([]Entry, 0, len(e.history)+len(e.active))
	workers := make([]int, 0, len(e.active))
	for worker := range e.active {
		workers = append(workers, worker)
	}
	sort.Ints(workers)
	for _, worker := range workers {
		a := e.active[worker]
		var n int64
		if st, err := os.Stat(a.file); err == nil {
			n = min(st.Size(), a.object.Size)
		}
		s.DownloadedBytes += n
		s.Entries = append(s.Entries, Entry{Key: a.object.Key, Status: "downloading", Size: a.object.Size, Bytes: n})
	}
	for i := len(e.history) - 1; i >= 0; i-- {
		s.Entries = append(s.Entries, e.history[i])
	}
	if s.Busy {
		s.Elapsed = time.Since(e.started).Seconds()
		if dt := time.Since(e.lastSample).Seconds(); dt >= 0.4 {
			instant := float64(max(int64(0), s.DownloadedBytes-e.lastBytes)) / dt
			if e.state.Speed == 0 {
				e.state.Speed = instant
			} else {
				e.state.Speed = .55*e.state.Speed + .45*instant
			}
			e.lastSample = time.Now()
			e.lastBytes = s.DownloadedBytes
		}
		s.Speed = e.state.Speed
		if s.TotalBytes > 0 {
			s.Progress = min(99.5, (float64(s.DownloadedBytes)*.8+float64(s.ArchivedBytes)*.2+float64(s.SkippedBytes))/float64(s.TotalBytes)*100)
		} else if s.TotalFiles > 0 {
			s.Progress = float64(s.ArchivedFiles+s.SkippedFiles) / float64(s.TotalFiles) * 99
		}
		if s.Speed > 0 {
			s.ETA = float64(max(int64(0), s.TotalBytes-s.DownloadedBytes-s.SkippedBytes)) / s.Speed
		}
	}
	return s
}

func (e *Engine) Test(c Config) error {
	source, err := NewCLI(c)
	if err != nil {
		return err
	}
	ctx, err := e.begin("checking", "Проверяем доступ к бакету…")
	if err != nil {
		return err
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		_, err := source.List(ctx, "", 1)
		e.finish(err, "Подключение успешно. Доступ к списку объектов подтверждён; файлы не скачивались.")
	}()
	return nil
}

func (e *Engine) Start(c Config) error {
	if err := c.Validate(true); err != nil {
		return err
	}
	source, err := NewCLI(c)
	if err != nil {
		return err
	}
	return e.StartSource(c, source)
}

// StartSource allows end-to-end verification against a local fixture, without touching S3.
func (e *Engine) StartSource(c Config, source Source) error {
	if err := c.Validate(true); err != nil {
		return err
	}
	ctx, err := e.begin("listing", "Считаем файлы и объём в S3…")
	if err != nil {
		return err
	}
	go func() {
		archive, err := e.run(ctx, c, source)
		if err == nil {
			e.mu.Lock()
			e.state.Archive = archive
			e.mu.Unlock()
		}
		e.finish(err, "Резервная копия готова. Все файлы сохранены в ZIP-архив.")
	}()
	return nil
}

func (e *Engine) record(obj Object, status string, reason ...string) {
	for i := range e.history {
		if e.history[i].Key == obj.Key {
			e.history = append(e.history[:i], e.history[i+1:]...)
			break
		}
	}
	entry := Entry{Key: obj.Key, Size: obj.Size, Bytes: obj.Size, Status: status}
	if status == "skipped" {
		entry.Bytes = 0
	}
	if len(reason) > 0 {
		entry.Error = reason[0]
	}
	e.history = append(e.history, entry)
	if len(e.history) > 100 {
		e.history = e.history[len(e.history)-100:]
	}
}

// Archive names must be safely extractable, including on Windows. Unusual S3 keys
// are placed under a stable numeric name; their original key is in the manifest.
func archiveName(key string, index int) string {
	valid := key != "" && utf8.ValidString(key) && !strings.HasPrefix(key, "/") && len(key) < 60000
	for _, part := range strings.Split(strings.TrimSuffix(key, "/"), "/") {
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		reserved := stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
			(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9')
		if part == "" || part == "." || part == ".." || reserved || strings.ContainsAny(part, "\\:<>\"|?*\x00") || strings.TrimRight(part, ". ") != part {
			valid = false
		}
		for _, r := range part {
			if r < 32 {
				valid = false
			}
		}
		units := 0
		for _, r := range part {
			units++
			if r > 0xffff {
				units++
			}
		}
		if units > 255 {
			valid = false
		}
	}
	if !valid {
		return fmt.Sprintf("objects/%09d", index)
	}
	return "files/" + key
}

type manifestEntry struct {
	Object
	ArchivePath string `json:"ArchivePath,omitempty"`
	Status      string `json:"Status"`
	Error       string `json:"Error,omitempty"`
}

type downloaded struct {
	index  int
	object Object
	file   string
	err    error
}

func (e *Engine) run(ctx context.Context, c Config, source Source) (result string, err error) {
	var objects []Object
	tokens := make(map[string]bool)
	for token := ""; ; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, err := source.List(ctx, token, 1000)
		if err != nil {
			return "", err
		}
		for _, obj := range page.Contents {
			if obj.Size < 0 {
				return "", fmt.Errorf("некорректный размер объекта %q", obj.Key)
			}
			objects = append(objects, obj)
			e.mu.Lock()
			e.state.TotalFiles++
			e.state.TotalBytes += obj.Size
			e.mu.Unlock()
		}
		if !page.IsTruncated {
			break
		}
		token = page.NextContinuationToken
		if token == "" || tokens[token] {
			return "", errors.New("S3 вернул некорректный токен следующей страницы")
		}
		tokens[token] = true
	}
	if err := checkSpace(c.Destination, objects, c.Parallel); err != nil {
		return "", err
	}
	workspace, err := os.MkdirTemp(c.Destination, ".s3-backup-")
	if err != nil {
		return "", fmt.Errorf("не удалось создать временную папку: %w", err)
	}
	defer os.RemoveAll(workspace)
	// Random suffix prevents collisions and overwrites even for repeated runs.
	filename := fmt.Sprintf("s3-%s-%s-%s.zip", safeLabel(c.Bucket), time.Now().Format("2006-01-02_15-04-05"), strings.TrimPrefix(filepath.Base(workspace), ".s3-backup-"))
	final := filepath.Join(c.Destination, filename)
	partial := final + ".partial"
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("не удалось создать архив: %w", err)
	}
	defer func() {
		f.Close()
		if result == "" {
			os.Remove(partial)
		}
	}()
	z := zip.NewWriter(f)
	if c.Compress {
		z.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestSpeed) })
	}
	manifest := make([]manifestEntry, len(objects))
	used := make(map[string]bool)
	requiredDirs := make(map[string]bool)
	for i, obj := range objects {
		name := archiveName(obj.Key, i)
		if obj.Size > 0 && strings.HasSuffix(name, "/") {
			continue
		}
		if strings.HasSuffix(name, "/") {
			requiredDirs[strings.ToLower(strings.TrimSuffix(name, "/"))] = true
		}
		for dir := path.Dir(strings.TrimSuffix(name, "/")); dir != "." && dir != "/"; dir = path.Dir(dir) {
			requiredDirs[strings.ToLower(dir)] = true
		}
	}
	for i, obj := range objects {
		name := archiveName(obj.Key, i)
		// S3 is case-sensitive; many Windows extractors aren't.
		folded := strings.ToLower(strings.TrimSuffix(name, "/"))
		if used[folded] || (!strings.HasSuffix(name, "/") && requiredDirs[folded]) || (obj.Size > 0 && strings.HasSuffix(name, "/")) {
			name = fmt.Sprintf("objects/%09d", i)
		}
		used[strings.ToLower(strings.TrimSuffix(name, "/"))] = true
		manifest[i] = manifestEntry{Object: obj, ArchivePath: name, Status: "pending"}
	}
	e.mu.Lock()
	e.state.Phase = "transferring"
	e.state.Message = "Скачиваем файлы и собираем ZIP-архив…"
	e.mu.Unlock()
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	ready := make(chan downloaded) // No unbounded on-disk download queue.
	var wg sync.WaitGroup
	var firstErr error
	var errOnce sync.Once
	fail := func(err error) { errOnce.Do(func() { firstErr = err; cancel() }) }
	for worker := 0; worker < c.Parallel; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range jobs {
				if workCtx.Err() != nil {
					return
				}
				obj := objects[i]
				target := filepath.Join(workspace, fmt.Sprintf("%09d.part", i))
				e.mu.Lock()
				e.active[worker] = active{object: obj, file: target}
				e.mu.Unlock()
				downloadErr := source.Download(workCtx, obj, target)
				if workCtx.Err() != nil {
					return
				}
				if downloadErr == nil {
					info, statErr := os.Stat(target)
					if statErr != nil {
						downloadErr = statErr
					} else if info.Size() != obj.Size {
						downloadErr = fmt.Errorf("размер изменился: ожидалось %d, получено %d", obj.Size, info.Size())
					}
				}
				e.mu.Lock()
				delete(e.active, worker)
				if downloadErr == nil {
					e.state.DownloadedFiles++
					e.state.DownloadedBytes += obj.Size
					e.record(obj, "downloaded")
				}
				e.mu.Unlock()
				select {
				case ready <- downloaded{index: i, object: obj, file: target, err: downloadErr}:
				case <-workCtx.Done():
					return
				}
			}
		}(worker)
	}
	go func() {
		defer close(jobs)
		for i := range objects {
			select {
			case jobs <- i:
			case <-workCtx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(ready) }()
	for item := range ready {
		if workCtx.Err() != nil {
			continue
		}
		obj := item.object
		if item.err != nil {
			// A failed object must leave neither a partial ZIP entry nor an unbounded spool.
			if err := os.Remove(item.file); err != nil && !errors.Is(err, os.ErrNotExist) {
				fail(fmt.Errorf("очистка временного файла %q: %w", obj.Key, err))
				continue
			}
			manifest[item.index].ArchivePath = ""
			manifest[item.index].Status = "skipped"
			manifest[item.index].Error = item.err.Error()
			e.mu.Lock()
			e.state.SkippedFiles++
			e.state.SkippedBytes += obj.Size
			e.record(obj, "skipped", item.err.Error())
			e.mu.Unlock()
			continue
		}
		e.mu.Lock()
		e.record(obj, "archiving")
		e.mu.Unlock()
		header := &zip.FileHeader{Name: manifest[item.index].ArchivePath, Method: zip.Store, Modified: obj.LastModified}
		if c.Compress {
			header.Method = zip.Deflate
		}
		w, err := z.CreateHeader(header)
		if err == nil {
			var input *os.File
			input, err = os.Open(item.file)
			if err == nil {
				_, err = io.Copy(w, &contextReader{ctx: workCtx, reader: input, onRead: func(n int) {
					e.mu.Lock()
					e.state.ArchivedBytes += int64(n)
					e.mu.Unlock()
				}})
				input.Close()
			}
		}
		if err != nil {
			fail(fmt.Errorf("архивация %q: %w", obj.Key, err))
			continue
		}
		if err := os.Remove(item.file); err != nil {
			fail(err)
			continue
		}
		e.mu.Lock()
		e.state.ArchivedFiles++
		e.record(obj, "archived")
		e.mu.Unlock()
		manifest[item.index].Status = "archived"
	}
	// ready is closed only after all workers exited: workspace cleanup cannot race them.
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if firstErr != nil {
		return "", firstErr
	}
	e.mu.Lock()
	e.state.Phase = "finalizing"
	e.state.Message = "Завершаем архив и сохраняем манифест…"
	e.mu.Unlock()
	w, err := z.Create("backup-manifest.json")
	if err != nil {
		return "", err
	}
	summary := e.Snapshot()
	err = json.NewEncoder(w).Encode(struct {
		Format        int             `json:"format"`
		Created       time.Time       `json:"created"`
		Bucket        string          `json:"bucket"`
		Endpoint      string          `json:"endpoint"`
		Prefix        string          `json:"prefix"`
		Objects       []manifestEntry `json:"objects"`
		Complete      bool            `json:"complete"`
		ArchivedFiles int             `json:"archivedFiles"`
		SkippedFiles  int             `json:"skippedFiles"`
	}{2, time.Now().UTC(), c.Bucket, c.Endpoint, c.Prefix, manifest, summary.SkippedFiles == 0, summary.ArchivedFiles, summary.SkippedFiles})
	if err != nil {
		return "", err
	}
	skipped := make([]manifestEntry, 0)
	for _, entry := range manifest {
		if entry.Status == "skipped" {
			skipped = append(skipped, entry)
		}
	}
	if len(skipped) > 0 {
		w, err := z.Create("skipped-files.json")
		if err != nil {
			return "", err
		}
		if err := json.NewEncoder(w).Encode(skipped); err != nil {
			return "", err
		}
	}
	if err := z.Close(); err != nil {
		return "", fmt.Errorf("завершение ZIP: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("сохранение архива: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(partial, final); err != nil {
		return "", err
	}
	return final, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
	onRead func(int)
}

func (r *contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(b)
	if n > 0 && r.onRead != nil {
		r.onRead(n)
	}
	return n, err
}

func safeLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "bucket"
	}
	return path.Base(b.String())
}

func checkSpace(destination string, objects []Object, parallel int) error {
	available, err := availableSpace(destination)
	if err != nil {
		return fmt.Errorf("не удалось проверить свободное место: %w", err)
	}
	var total int64
	sizes := make([]int64, len(objects))
	for i, obj := range objects {
		total += obj.Size
		sizes[i] = obj.Size
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] })
	// ZIP + bounded spool + allowance for central directory, manifest and deflate overhead.
	required := total + total/100 + int64(len(objects))*2048 + 32*1024*1024
	for i := 0; i < min(parallel+1, len(sizes)); i++ {
		required += sizes[i]
	}
	if available < uint64(required) {
		return fmt.Errorf("недостаточно свободного места: доступно %.1f ГБ, для архива и временных загрузок нужно до %.1f ГБ", float64(available)/(1<<30), float64(required)/(1<<30))
	}
	return nil
}
