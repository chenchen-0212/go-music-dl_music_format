//go:build !windows

package mediaassets

import (
	"io/fs"
)

func windowsAssetFS() (fs.FS, bool) {
	return nil, false
}
