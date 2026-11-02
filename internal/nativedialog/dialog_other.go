//go:build !(linux || freebsd || openbsd)

package nativedialog

import (
	"github.com/ncruces/zenity"
)

// available : ailleurs, la bibliothèque parle directement au système (COM sous
// Windows, osascript sous macOS), il n'y a rien à chercher.
func available() error { return nil }

func openFile(title string, filters ...Filter) (string, error) {
	path, err := zenity.SelectFile(append([]zenity.Option{zenity.Title(title)}, toZenity(filters)...)...)
	return path, describe(err)
}

func openFolder(title string) (string, error) {
	path, err := zenity.SelectFile(zenity.Title(title), zenity.Directory())
	return path, describe(err)
}
