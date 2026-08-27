package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultConcurrency = 3
	maxConcurrency     = 8
	requestLimit       = 1000
	queueCapacity      = 10000
	eventBuffer        = 256
)

var supportedInputs = map[string]bool{
	".flac": true,
	".wav":  true,
	".m4a":  true,
	".aac":  true,
	".ogg":  true,
	".wma":  true,
}

var supportedBitrates = map[string]bool{
	Bitrate128K: true,
	Bitrate192K: true,
	Bitrate256K: true,
	Bitrate320K: true,
}

var (
	ErrEmptyRequest        = errors.New("converter: no input files")
	ErrTooManyFiles        = errors.New("converter: too many input files")
	ErrInvalidInput        = errors.New("converter: unsupported audio format")
	ErrInputNotFound       = errors.New("converter: input file does not exist")
	ErrInvalidBitrate      = errors.New("converter: unsupported bitrate")
	ErrUnsupportedFormat   = errors.New("converter: unsupported output format")
	ErrInvalidConflictMode = errors.New("converter: invalid conflict policy")
	ErrTaskNotFound        = errors.New("converter: task not found")
	ErrTaskNotCancellable  = errors.New("converter: task is not running or pending")
	ErrTaskNotRetryable    = errors.New("converter: task is not failed or cancelled")
)

type CreateRequest struct {
	Files          []string       `json:"files"`
	Format         string         `json:"format"`
	Bitrate        string         `json:"bitrate"`
	OutputDir      string         `json:"outputDir"`
	ConflictPolicy ConflictPolicy `json:"conflictPolicy"`
}

type eventSubscription struct {
	id string
	ch chan ConvertTask
}

type conversionFunc func(context.Context, *ConvertTask, func(int64)) error

type Service struct {
	mu       sync.RWMutex
	tasks    map[string]*ConvertTask
	order    []string
	queue    chan string
	cancels  map[string]context.CancelFunc
	listener *sync.Map
	execute  conversionFunc
	closed   bool
}

func NewService(concurrency int) *Service {
	return NewServiceWithRunner(concurrency, RunFFmpeg)
}

// NewServiceWithRunner is used by unit tests so batch scheduling can be tested
// without requiring FFmpeg binaries on the build host.
func NewServiceWithRunner(concurrency int, execute conversionFunc) *Service {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if concurrency > maxConcurrency {
		concurrency = maxConcurrency
	}
	if execute == nil {
		panic("converter: conversion function is required")
	}

	service := &Service{
		tasks:    make(map[string]*ConvertTask),
		queue:    make(chan string, queueCapacity),
		cancels:  make(map[string]context.CancelFunc),
		listener: &sync.Map{},
		execute:  execute,
	}
	for i := 0; i < concurrency; i++ {
		go service.worker()
	}
	return service
}

func (s *Service) CreateTasks(req CreateRequest) ([]ConvertTask, error) {
	req.Format = strings.ToLower(strings.TrimSpace(req.Format))
	if req.Format == "" {
		req.Format = OutputFormatMP3
	}
	if req.Format != OutputFormatMP3 {
		return nil, ErrUnsupportedFormat
	}
	req.Bitrate = strings.TrimSpace(req.Bitrate)
	if req.Bitrate == "" {
		req.Bitrate = Bitrate320K
	}
	if !supportedBitrates[req.Bitrate] {
		return nil, ErrInvalidBitrate
	}
	if req.ConflictPolicy == "" {
		req.ConflictPolicy = ConflictRename
	}
	if req.ConflictPolicy != ConflictRename && req.ConflictPolicy != ConflictSkip {
		return nil, ErrInvalidConflictMode
	}
	if len(req.Files) == 0 {
		return nil, ErrEmptyRequest
	}
	if len(req.Files) > requestLimit {
		return nil, ErrTooManyFiles
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("converter: service is shutting down")
	}

	now := time.Now()
	outputExt := "." + req.Format
	nameCounts := make(map[string]int)
	planned := make(map[string]bool)
	result := make([]ConvertTask, 0, len(req.Files))

	for _, rawFile := range req.Files {
		input := filepath.Clean(strings.TrimSpace(rawFile))
		var inputErr error
		info, statErr := os.Stat(input)
		switch {
		case input == "" || input == ".":
			continue
		case statErr != nil:
			inputErr = fmt.Errorf("%w: %s", ErrInputNotFound, filepath.Base(input))
		case info.IsDir():
			inputErr = errors.New("converter: input path is a directory")
		case !supportedInputs[strings.ToLower(filepath.Ext(input))]:
			inputErr = fmt.Errorf("%w: %s", ErrInvalidInput, filepath.Base(input))
		}

		filename := filepath.Base(input)
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		outputDir := filepath.Dir(input)
		if custom := strings.TrimSpace(req.OutputDir); custom != "" {
			outputDir = filepath.Clean(custom)
		}

		queueKey := strings.ToLower(filepath.ToSlash(outputDir)) + "\x00" + strings.ToLower(base)
		output, nextCount := uniqueOutput(
			outputDir,
			base,
			outputExt,
			nameCounts[queueKey],
			req.ConflictPolicy,
			planned,
		)
		nameCounts[queueKey] = nextCount
		planned[strings.ToLower(filepath.Clean(output))] = true

		task := &ConvertTask{
			ID:             newTaskID(),
			Input:          input,
			InputName:      filepath.Base(input),
			Output:         output,
			Format:         req.Format,
			Bitrate:        req.Bitrate,
			ConflictPolicy: req.ConflictPolicy,
			Status:         StatusPending,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if inputErr != nil {
			task.Status = StatusFailed
			task.Error = inputErr.Error()
		}
		s.tasks[task.ID] = task
		s.order = append(s.order, task.ID)
		result = append(result, *cloneTask(task))
		if inputErr == nil {
			s.queue <- task.ID
		}
	}

	if len(result) == 0 {
		return nil, ErrInvalidInput
	}
	return result, nil
}

func (s *Service) ListTasks() []ConvertTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ConvertTask, 0, len(s.order))
	for _, id := range s.order {
		if task := s.tasks[id]; task != nil {
			result = append(result, *cloneTask(task))
		}
	}
	return result
}

// FilesFromDirectory expands a user-selected folder into supported inputs so
// the HTTP layer does not perform filesystem traversal itself.
func (s *Service) FilesFromDirectory(root string) ([]string, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	info, err := os.Stat(cleanRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("converter: selected path is not a directory")
	}

	var files []string
	err = filepath.WalkDir(cleanRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !supportedInputs[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		files = append(files, filepath.Clean(path))
		if len(files) > requestLimit {
			return ErrTooManyFiles
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrInvalidInput
	}
	return files, nil
}

func (s *Service) GetTask(id string) (ConvertTask, error) {
	s.mu.RLock()
	task, ok := s.tasks[id]
	s.mu.RUnlock()
	if !ok {
		return ConvertTask{}, ErrTaskNotFound
	}
	return *cloneTask(task), nil
}

func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return ErrTaskNotFound
	}
	switch task.Status {
	case StatusPending:
		setTaskState(task, StatusCancelled, "")
	case StatusRunning:
		cancel, exists := s.cancels[id]
		if !exists {
			return ErrTaskNotCancellable
		}
		setTaskState(task, StatusCancelled, "")
		cancel()
	default:
		return ErrTaskNotCancellable
	}

	s.emitLocked(*cloneTask(task))
	return nil
}

func (s *Service) Retry(id string) error {
	s.mu.Lock()
	task, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return ErrTaskNotFound
	}
	if task.Status != StatusFailed && task.Status != StatusCancelled {
		s.mu.Unlock()
		return ErrTaskNotRetryable
	}

	setTaskState(task, StatusPending, "")
	task.Progress = 0
	task.Skipped = false
	task.DurationMillis = nil
	task.StartedAt = nil
	task.FinishedAt = nil
	current := *cloneTask(task)
	select {
	case s.queue <- id:
	default:
		setTaskState(task, StatusFailed, "converter: queue is full")
		s.mu.Unlock()
		return errors.New("converter: queue is full")
	}
	s.emitLocked(current)
	s.mu.Unlock()
	return nil
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	task, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return ErrTaskNotFound
	}
	if task.Status == StatusRunning || task.Status == StatusPending {
		s.mu.Unlock()
		return errors.New("converter: stop the task before deleting it")
	}
	delete(s.tasks, id)
	for i, itemID := range s.order {
		if itemID == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) Events() (<-chan ConvertTask, func()) {
	ch := make(chan ConvertTask, eventBuffer)
	id := strconvFormatUint(uint64(nextSubscriberID.Add(1)))
	sub := &eventSubscription{id: id, ch: ch}
	s.listener.Store(id, sub)
	unsubscribe := func() { s.listener.Delete(id) }

	for _, task := range s.ListTasks() {
		select {
		case ch <- task:
		default:
		}
	}
	return ch, unsubscribe
}

func (s *Service) reportProgress(id string, microseconds int64) {
	progress := float64(microseconds) / 10_000 // microsecond -> percent.
	if progress < 0 {
		progress = 0
	} else if progress > 100 {
		progress = 100
	}

	s.updateTask(id, func(task *ConvertTask) {
		task.DurationMillis = int64Ptr(microseconds / 1000)
		task.Progress = progress
		task.UpdatedAt = time.Now()
	})
}

func (s *Service) updateTask(id string, mutate func(*ConvertTask)) {
	s.mu.Lock()
	task, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	mutate(task)
	current := *cloneTask(task)
	s.emitLocked(current)
	s.mu.Unlock()
}

func (s *Service) emitLocked(task ConvertTask) {
	s.listener.Range(func(_, value any) bool {
		if sub, ok := value.(*eventSubscription); ok && sub != nil {
			select {
			case sub.ch <- task:
			default:
				// A slow browser must not stall conversion workers.
			}
		}
		return true
	})
}

func uniqueOutput(
	dir string,
	base string,
	ext string,
	count int,
	policy ConflictPolicy,
	planned map[string]bool,
) (string, int) {
	first := filepath.Join(dir, base+ext)
	if policy == ConflictSkip && count == 0 {
		return first, count + 1
	}

	candidate := first
	for fileExists(candidate) || planned[strings.ToLower(candidate)] {
		count++
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, count, ext))
	}
	return candidate, count + 1
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func setTaskState(task *ConvertTask, status, message string) {
	task.Status = status
	task.Error = message
	task.UpdatedAt = time.Now()
}
