package converter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Service) worker() {
	for id := range s.queue {
		s.runOne(context.Background(), id)
	}
}

func (s *Service) runOne(parent context.Context, id string) {
	s.mu.Lock()
	task := s.tasks[id]
	if task == nil || task.Status != StatusPending || s.closed {
		s.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(parent)
	startedAt := timeNow()
	task.Status = StatusRunning
	task.Error = ""
	task.Skipped = false
	task.Progress = 0
	startedCopy := startedAt
	task.StartedAt = &startedCopy
	task.FinishedAt = nil
	task.UpdatedAt = startedAt
	s.cancels[id] = cancel
	runningEvent := *cloneTask(task)
	s.emitLocked(runningEvent)
	s.mu.Unlock()

	report := func(microseconds int64) {
		s.reportProgress(id, microseconds)
	}
	err := s.prepareConversion(ctx, task)
	if err == nil {
		err = s.execute(ctx, task, report)
	}

	s.mu.Lock()
	delete(s.cancels, id)

	finishedAt := timeNow()
	finishedCopy := finishedAt
	task.FinishedAt = &finishedCopy
	switch {
	case task.Status == StatusCancelled:
		// Cancel changed the state while FFmpeg was still shutting down.
	case errors.Is(err, context.Canceled):
		setTaskState(task, StatusCancelled, "")
	case err != nil:
		setTaskState(task, StatusFailed, err.Error())
		task.Progress = 0
	default:
		setTaskState(task, StatusSuccess, "")
		task.Progress = 100
	}
	finalEvent := *cloneTask(task)
	s.emitLocked(finalEvent)
	s.mu.Unlock()
}

func (s *Service) prepareConversion(ctx context.Context, task *ConvertTask) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(task.Output), 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if _, err := os.Stat(task.Input); err != nil {
		return fmt.Errorf("input file does not exist: %w", err)
	}
	if task.ConflictPolicy == ConflictSkip && fileExists(task.Output) {
		task.Skipped = true
		return nil
	}
	return nil
}
