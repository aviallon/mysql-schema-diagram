package nativedialog

import "errors"

// errPortalUnavailable marque l'absence du portail XDG, et rien d'autre : c'est
// le seul cas où l'on retombe sur kdialog ou zenity. Une annulation, ou une
// erreur réelle du portail, doit remonter telle quelle — sinon le repli
// rouvrirait un dialogue par-dessus celui que l'utilisateur vient de fermer.
var errPortalUnavailable = errors.New("portail XDG indisponible")
