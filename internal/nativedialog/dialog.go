package nativedialog

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ncruces/zenity"
)

// Filter restreint les fichiers proposés. Patterns suit la syntaxe du système
// (« *.sql »), Empty est le libellé affiché pour l'option « tous les fichiers ».
type Filter struct {
	Label    string
	Patterns []string
}

// OpenFile fait choisir un fichier existant et renvoie son chemin.
func OpenFile(title string, filters ...Filter) (string, error) {
	if err := available(); err != nil {
		return "", err
	}
	path, err := openFile(title, filters...)
	return path, describe(err)
}

// OpenFolder fait choisir un dossier existant et renvoie son chemin.
func OpenFolder(title string) (string, error) {
	if err := available(); err != nil {
		return "", err
	}
	path, err := openFolder(title)
	return path, describe(err)
}

// toZenity traduit nos filtres vers ceux de la bibliothèque.
func toZenity(filters []Filter) []zenity.Option {
	if len(filters) == 0 {
		return nil
	}
	converted := make(zenity.FileFilters, 0, len(filters))
	for _, f := range filters {
		converted = append(converted, zenity.FileFilter{
			Name:     f.Label,
			Patterns: f.Patterns,
		})
	}
	return []zenity.Option{converted}
}

// describe rend l'erreur exploitable par l'appelant, qui l'affiche tel quel.
func describe(err error) error {
	if err == nil {
		return nil
	}
	// L'annulation n'est pas une erreur : l'utilisateur a simplement changé
	// d'avis, et afficher un message rouge pour cela serait trompeur. On accepte
	// aussi bien le sentinelle de la bibliothèque que sa reformulation.
	if errors.Is(err, zenity.ErrCanceled) || strings.Contains(err.Error(), "canceled") {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "executable file not found") {
		return fmt.Errorf("aucun dialogue système disponible : installez zenity ou kdialog")
	}
	return fmt.Errorf("sélection de fichier : %w", err)
}
