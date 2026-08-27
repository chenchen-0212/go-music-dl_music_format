//go:build windows

package mediaassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupReleasesWindowsFFmpeg(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	if err := Setup(); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	for _, binary := range binaries {
		info, err := os.Stat(filepath.Join(root, "MusicDL", targetBinDirName, binary.fileName))
		if err != nil || info.Size() < 1024*1024 {
			t.Fatalf("%s was not released: %v", binary.fileName, err)
		}
	}
	version := readSmallFile(filepath.Join(root, "MusicDL", targetBinDirName, "VERSION"))
	if !strings.Contains(string(version), embeddedFFmpegVersion) {
		t.Fatalf("VERSION = %q, want %q", string(version), embeddedFFmpegVersion)
	}
}
