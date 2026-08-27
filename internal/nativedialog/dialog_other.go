//go:build !windows

package nativedialog

import "errors"

var ErrUnavailable = errors.New("native dialogs are only available on Windows")

func PickFiles(title string) ([]string, error) {
	return nil, ErrUnavailable
}

func PickFolder(title string) (string, error) {
	return "", ErrUnavailable
}
