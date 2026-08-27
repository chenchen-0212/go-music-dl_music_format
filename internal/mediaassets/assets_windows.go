//go:build windows

package mediaassets

import (
	"embed"
	"io/fs"
)

//go:embed all:binaries/windows/amd64
var windowsAssets embed.FS

func windowsAssetFS() (fs.FS, bool) {
	sub, err := fs.Sub(windowsAssets, ".")
	if err != nil {
		return nil, false
	}
	return sub, true
}
