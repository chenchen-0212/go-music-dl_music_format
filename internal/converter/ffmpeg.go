package converter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/guohuiyuan/go-music-dl/core"
)

// RunFFmpeg transcodes one input to MP3 while preserving format-level metadata.
// report receives the elapsed playback position in microseconds from FFmpeg's
// progress protocol; total duration is not needed to show elapsed percentage.
func RunFFmpeg(ctx context.Context, task *ConvertTask, report func(int64)) error {
	ffmpegPath, err := core.ResolveFFmpegPath()
	if err != nil {
		return fmt.Errorf("FFmpeg is not available: %w", err)
	}

	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "info",
		"-progress", "pipe:1",
		"-nostats",
		"-i", task.Input,
		"-map", "0:a:0",
		"-map", "0:v:0?",
		"-map_metadata", "0",
		"-codec:a", "libmp3lame",
		"-b:a", task.Bitrate,
		"-c:v", "copy",
		"-disposition:v:0", "attached_pic",
		task.Output,
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	core.HideCommandWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open FFmpeg stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open FFmpeg stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start FFmpeg: %w", err)
	}

	var stderrTail strings.Builder
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		copyErrorTail(&stderrTail, stderr)
	}()

	reader := bufio.NewScanner(stdout)
	reader.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	progress := handleProgressLine(report)
	for reader.Scan() {
		progress(reader.Text())
	}
	runErr := cmd.Wait()
	<-stderrDone

	if ctx.Err() != nil {
		removePartialOutput(task.Output)
		return ctx.Err()
	}
	if runErr != nil {
		removePartialOutput(task.Output)
		message := strings.TrimSpace(stderrTail.String())
		if message == "" {
			message = runErr.Error()
		}
		return fmt.Errorf("FFmpeg conversion failed: %s", message)
	}
	info, statErr := os.Stat(task.Output)
	if statErr != nil || info.Size() <= 0 {
		return errors.New("FFmpeg did not produce an audio file")
	}
	return nil
}

func copyErrorTail(dst *strings.Builder, reader io.Reader) {
	const tailBytes = 8 * 1024
	data, _ := io.ReadAll(reader)
	if len(data) > tailBytes {
		data = data[len(data)-tailBytes:]
	}
	dst.Write(data)
	dst.WriteString("\n")
}

func handleProgressLine(report func(int64)) func(string) {
	return func(line string) {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			return
		}
		switch key {
		case "out_time_us", "out_time_ms":
			// Despite its name, modern FFmpeg reports out_time_ms in microseconds.
			micros, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err == nil && micros >= 0 {
				report(micros)
			}
		}
	}
}

func removePartialOutput(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
