package converter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not reached before timeout")
}

func TestBatchConversionLimitsConcurrentWorkers(t *testing.T) {
	var current, maximum, completed atomic.Int64
	converter := NewServiceWithRunner(DefaultConcurrency, func(context.Context, *ConvertTask, func(int64)) error {
		active := current.Add(1)
		for {
			oldMax := maximum.Load()
			if active <= oldMax || maximum.CompareAndSwap(oldMax, active) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		current.Add(-1)
		completed.Add(1)
		return nil
	})

	dir := t.TempDir()
	files := make([]string, 0, 8)
	for i := range 8 {
		path := filepath.Join(dir, strings.Repeat("a", i+1)+".flac")
		if err := os.WriteFile(path, []byte("audio"), 0644); err != nil {
			t.Fatalf("write input: %v", err)
		}
		files = append(files, path)
	}

	tasks, err := converter.CreateTasks(CreateRequest{
		Files:   files,
		Bitrate: Bitrate192K,
	})
	if err != nil {
		t.Fatalf("CreateTasks() error = %v", err)
	}
	if len(tasks) != len(files) {
		t.Fatalf("task count = %d, want %d", len(tasks), len(files))
	}
	if tasks[0].Bitrate != Bitrate192K {
		t.Fatalf("bitrate = %q", tasks[0].Bitrate)
	}

	waitFor(t, 3*time.Second, func() bool {
		return int(completed.Load()) == len(files)
	})
	if maximum.Load() > DefaultConcurrency {
		t.Fatalf("maximum concurrent conversions = %d, want <= %d", maximum.Load(), DefaultConcurrency)
	}
	for _, item := range converter.ListTasks() {
		if item.Status != StatusSuccess || item.Progress != 100 {
			t.Fatalf("task %s: status=%s progress=%.0f", item.ID, item.Status, item.Progress)
		}
	}
}

func TestCancelPendingTaskBeforeWorkerStarts(t *testing.T) {
	started := make(chan string, 1)
	releaseFirst := make(chan struct{})
	closed := false
	defer func() {
		if !closed {
			close(releaseFirst)
		}
	}()

	converter := NewServiceWithRunner(1, func(_ context.Context, task *ConvertTask, _ func(int64)) error {
		started <- filepath.Base(task.Input)
		if strings.HasSuffix(task.Input, "first.flac") {
			<-releaseFirst
		}
		return nil
	})

	dir := t.TempDir()
	first := filepath.Join(dir, "first.flac")
	second := filepath.Join(dir, "second.flac")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("audio"), 0644); err != nil {
			t.Fatalf("write input: %v", err)
		}
	}
	tasks, err := converter.CreateTasks(CreateRequest{Files: []string{first, second}})
	if err != nil {
		t.Fatalf("CreateTasks() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first conversion did not start")
	}
	waitFor(t, time.Second, func() bool {
		state, err := converter.GetTask(tasks[1].ID)
		return err == nil && state.Status == StatusPending
	})

	if err := converter.Cancel(tasks[1].ID); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	close(releaseFirst)
	closed = true

	waitFor(t, 2*time.Second, func() bool {
		firstState, err := converter.GetTask(tasks[0].ID)
		secondState, secondErr := converter.GetTask(tasks[1].ID)
		return err == nil && firstState.Status == StatusSuccess &&
			secondErr == nil && secondState.Status == StatusCancelled
	})
}

func TestInvalidPathsBecomeFailedTasksWithErrors(t *testing.T) {
	converter := NewServiceWithRunner(1, func(context.Context, *ConvertTask, func(int64)) error {
		t.Fatal("invalid inputs must not be converted")
		return nil
	})
	dir := t.TempDir()
	unsupported := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(unsupported, []byte("text"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tasks, err := converter.CreateTasks(CreateRequest{
		Files: []string{
			filepath.Join(dir, "missing.flac"),
			unsupported,
		},
	})
	if err != nil {
		t.Fatalf("CreateTasks() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count = %d, want 2", len(tasks))
	}
	for _, task := range tasks {
		if task.Status != StatusFailed || task.Error == "" {
			t.Fatalf("task %s should fail with an explanation: %+v", task.InputName, task)
		}
	}
	if !errors.Is(errors.New(tasks[0].Error), ErrInputNotFound) &&
		!strings.Contains(tasks[0].Error, "input file does not exist") {
		t.Fatalf("missing-file error = %q", tasks[0].Error)
	}
	if !strings.Contains(tasks[1].Error, "unsupported audio format") {
		t.Fatalf("unsupported-format error = %q", tasks[1].Error)
	}
}

func TestUniqueOutputAvoidsOverwritesAndDuplicatesInOneBatch(t *testing.T) {
	outputDir := t.TempDir()
	inputDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outputDir, "song.mp3"), []byte("old"), 0644); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	first := filepath.Join(inputDir, "one", "song.flac")
	second := filepath.Join(inputDir, "two", "song.flac")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("audio"), 0644); err != nil {
			t.Fatalf("write input: %v", err)
		}
	}

	service := &Service{tasks: make(map[string]*ConvertTask)}
	planned := make(map[string]bool)
	firstOut, nextCount := uniqueOutput(outputDir, "song", ".mp3", 0, ConflictRename, planned)
	secondOut, _ := uniqueOutput(outputDir, "song", ".mp3", nextCount, ConflictRename, planned)
	planned[strings.ToLower(firstOut)] = true // mirrors CreateTasks deduplication.
	_, _ = service, os.MkdirAll(outputDir, 0755)

	if firstOut != filepath.Join(outputDir, "song (1).mp3") {
		t.Fatalf("first output = %q", firstOut)
	}
	if secondOut == firstOut || secondOut == filepath.Join(outputDir, "song.mp3") {
		t.Fatalf("second output collides: %q", secondOut)
	}
}

func TestConflictPolicyValidationAndClone(t *testing.T) {
	task := &ConvertTask{DurationMillis: int64Ptr(12), StartedAt: &[]time.Time{time.Unix(1, 0)}[0]}
	cloned := cloneTask(task)
	*cloned.DurationMillis = 99
	if *task.DurationMillis != 12 {
		t.Fatal("cloneTask did not deep-copy DurationMillis")
	}

	service := NewServiceWithRunner(1, func(context.Context, *ConvertTask, func(int64)) error { return nil })
	if _, err := service.CreateTasks(CreateRequest{Files: []string{"x.flac"}, ConflictPolicy: ConflictPolicy("other")}); !errors.Is(err, ErrInvalidConflictMode) {
		t.Fatalf("error = %v, want ErrInvalidConflictMode", err)
	}
}
