//go:build !linux

package nativedialog

// Le portail XDG n'existe que sur Linux : ailleurs, c'est le dialogue du système
// qui est appelé directement (voir dialog_other.go), sans repli à prévoir.
func portalOpen(title string, dir bool, filters []Filter) (string, error) {
	return "", errPortalUnavailable
}
