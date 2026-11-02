//go:build linux || freebsd || openbsd

package nativedialog

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/ncruces/zenity"
)

// Trois fournisseurs, essayés dans cet ordre :
//
//  1. le portail XDG — le dialogue du bureau, celui que reçoit aussi une
//     application empaquetée en Flatpak, et qui n'exige aucun binaire annexe ;
//  2. kdialog — le dialogue natif de KDE, quand le portail n'est pas là ;
//  3. zenity, ou qarma, ou matedialog — pour les bureaux plus anciens.
//
// Le repli n'a lieu que si le fournisseur précédent est *absent*. Une
// annulation, ou une erreur réelle, remonte telle quelle : retomber sur le
// fournisseur suivant rouvrirait un dialogue par-dessus celui que l'utilisateur
// vient de fermer.
func openFile(title string, filters ...Filter) (string, error) {
	if path, err := portalOpen(title, false, filters); !errors.Is(err, errPortalUnavailable) {
		return path, err
	}
	if path, err := kdialog(); err == nil {
		return runKDialog(path, "--title", title, "--getopenfilename", "", filterSpec(filters))
	}
	return zenity.SelectFile(append([]zenity.Option{zenity.Title(title)}, toZenity(filters)...)...)
}

func openFolder(title string) (string, error) {
	if path, err := portalOpen(title, true, nil); !errors.Is(err, errPortalUnavailable) {
		return path, err
	}
	if path, err := kdialog(); err == nil {
		return runKDialog(path, "--title", title, "--getexistingdirectory", "")
	}
	return zenity.SelectFile(zenity.Title(title), zenity.Directory())
}

// runKDialog lance kdialog avec l'action demandée. kdialog sort en 1 quand
// l'utilisateur annule ; ce n'est pas une erreur, et cela ne doit surtout pas
// déclencher le repli suivant.
func runKDialog(path string, args ...string) (string, error) {
	out, err := exec.Command(path, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", zenity.ErrCanceled
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func kdialog() (string, error) { return exec.LookPath("kdialog") }

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
