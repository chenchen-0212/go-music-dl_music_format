// Package mediaassets releases FFmpeg binaries that were embedded at build
// time. Splitting this from core keeps the media logic free of asset plumbing.
package mediaassets

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	embeddedFFmpegVersion = "7.1.1-essentials"
	targetBinDirName      = "bin"
)

var ErrNotEmbedded = errors.New("FFmpeg assets are not embedded in this build")

type namedBinary struct {
	assetName string
	envName   string
	fileName  string
}

var binaries = []namedBinary{
	{assetName: "ffmpeg.exe", envName: "MUSIC_DL_FFMPEG", fileName: "ffmpeg.exe"},
	{assetName: "ffprobe.exe", envName: "MUSIC_DL_FFPROBE", fileName: "ffprobe.exe"},
}

func Setup() error {
	source, ok := windowsAssetFS()
	if !ok {
		return ErrNotEmbedded
	}

	rootDir, err := localAppData()
	if err != nil {
		return fmt.Errorf("resolve local application data: %w", err)
	}
	binDir := filepath.Join(rootDir, "MusicDL", targetBinDirName)
	versionFile := filepath.Join(binDir, "VERSION")

	currentVersion := strings.TrimSpace(string(readSmallFile(versionFile)))
	if currentVersion == embeddedFFmpegVersion {
		configureEnvironment(binDir)
		return nil
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create FFmpeg directory: %w", err)
	}
	for _, binary := range binaries {
		data, err := fs.ReadFile(source, "binaries/windows/amd64/"+binary.assetName)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", binary.fileName, err)
		}
		if len(data) < 1024*1024 {
			return fmt.Errorf("embedded %s is invalid", binary.fileName)
		}
		if err := atomicWrite(filepath.Join(binDir, binary.fileName), data, 0755); err != nil {
			return fmt.Errorf("release %s: %w", binary.fileName, err)
		}
	}

	license, licenseErr := fs.ReadFile(source, "binaries/windows/amd64/LICENSE.txt")
	if licenseErr == nil && len(license) > 0 {
		_ = atomicWrite(filepath.Join(binDir, "FFmpeg-LICENSE.txt"), license, 0644)
	}
	if err := atomicWrite(versionFile, []byte(embeddedFFmpegVersion+"\n"), 0644); err != nil {
		return fmt.Errorf("write FFmpeg version: %w", err)
	}

	configureEnvironment(binDir)
	return nil
}

func configureEnvironment(binDir string) {
	for _, binary := range binaries {
		path := filepath.Join(binDir, binary.fileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			_ = os.Setenv(binary.envName, path)
		}
	}
	prependEnvPath("PATH", binDir)
}

func prependEnvPath(name, dir string) {
	current := os.Getenv(name)
	for _, item := range filepath.SplitList(current) {
		if filepath.Clean(item) == filepath.Clean(dir) {
			return
		}
	}
	if current == "" {
		_ = os.Setenv(name, dir)
		return
	}
	_ = os.Setenv(name, dir+string(os.PathListSeparator)+current)
}

func localAppData() (string, error) {
	if path := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); path != "" {
		return filepath.Clean(path), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return base, nil
}

func readSmallFile(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	data, _ := io.ReadAll(io.LimitReader(file, 4096))
	return data
}

func atomicWrite(target string, data []byte, mode fs.FileMode) error {
	temp := target + ".tmp"
	if err := os.WriteFile(temp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(temp, mode); err != nil {
		_ = os.Remove(temp)
		return err
	}
	// Windows rename does not replace an executable currently loaded by an old
	// process. Removing first keeps upgrades simple when the previous instance
	// has already exited.
	_ = os.Remove(target)
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
