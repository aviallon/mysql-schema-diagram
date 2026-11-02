//go:build linux || freebsd || openbsd

package nativedialog

import (
	"os/exec"
	"strings"

	"github.com/ncruces/zenity"
)

// available vérifie qu'un dialogue est utilisable. kdialog est préféré sur les
// bureaux KDE, où c'est le dialogue natif ; la bibliothèque, elle, ne connaît
// que zenity, qarma et matedialog.
func available() error {
	if _, err := kdialog(); err == nil {
		return nil
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		return nil
	}
	return zenity.ErrUnsupported
}

func kdialog() (string, error) { return exec.LookPath("kdialog") }

func openFile(title string, filters ...Filter) (string, error) {
	if path, err := kdialog(); err == nil {
		args := []string{"--title", title, "--getopenfilename", "", filterSpec(filters)}
		out, err := exec.Command(path, args...).Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
				return "", zenity.ErrCanceled // annulation : ce n'est pas une erreur
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	return zenity.SelectFile(append([]zenity.Option{zenity.Title(title)}, toZenity(filters)...)...)
}

func openFolder(title string) (string, error) {
	if path, err := kdialog(); err == nil {
		out, err := exec.Command(path, "--title", title, "--getexistingdirectory", "").Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
				return "", zenity.ErrCanceled
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	return zenity.SelectFile(zenity.Title(title), zenity.Directory())
}

// filterSpec construit le motif attendu par kdialog : « *.sql|Fichiers SQL ».
func filterSpec(filters []Filter) string {
	if len(filters) == 0 {
		return "*"
	}
	parts := make([]string, 0, len(filters))
	for _, f := range filters {
		parts = append(parts, strings.Join(f.Patterns, " ")+"|"+f.Label)
	}
	return strings.Join(parts, "\n")
}
