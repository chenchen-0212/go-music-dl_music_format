package converter

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/guohuiyuan/go-music-dl/core"
)

func TestFFmpegConvertsWAVToMP3(t *testing.T) {
	ffmpegPath, err := core.ResolveFFmpegPath()
	if err != nil {
		candidates := []string{
			filepath.Join(os.TempDir(), "ffextract711", "ffmpeg-7.1.1-essentials_build", "bin", "ffmpeg.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "MusicDL", "bin", "ffmpeg.exe"),
		}
		var located string
		for _, candidate := range candidates {
			if fileExists(candidate) {
				located = candidate
				break
			}
		}
		if located != "" {
			t.Setenv("MUSIC_DL_FFMPEG", located)
			ffmpegPath = located
		} else {
			t.Skip("FFmpeg is unavailable")
		}
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "sample.wav")
	outputDir := filepath.Join(dir, "mp3")
	generate := exec.Command(ffmpegPath, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1", input)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate WAV: %v\n%s", err, out)
	}

	service := NewServiceWithRunner(1, RunFFmpeg)
	created, err := service.CreateTasks(CreateRequest{
		Files:          []string{input},
		OutputDir:      outputDir,
		Bitrate:        Bitrate320K,
		ConflictPolicy: ConflictRename,
	})
	if err != nil {
		t.Fatalf("CreateTasks() error = %v", err)
	}

	waitFor(t, 15*time.Second, func() bool {
		state, err := service.GetTask(created[0].ID)
		return err == nil && (state.Status == StatusSuccess || state.Status == StatusFailed)
	})
	state, err := service.GetTask(created[0].ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if state.Status != StatusSuccess {
		t.Fatalf("status=%s error=%q", state.Status, state.Error)
	}
	info, statErr := os.Stat(state.Output)
	if statErr != nil {
		t.Fatalf("MP3 output stat: %v", statErr)
	}
	if info.Size() <= 1024 {
		t.Fatalf("MP3 output is too small: %d bytes", info.Size())
	}
}
