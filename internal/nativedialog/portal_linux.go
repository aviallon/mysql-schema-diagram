//go:build linux

package nativedialog

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/ncruces/zenity"
)

// Le portail XDG (org.freedesktop.portal.FileChooser, sur le bus de session)
// est la voie normale sur Linux : c'est le dialogue du bureau, celui que
// reçoivent aussi les applications empaquetées en Flatpak, et il ne demande
// aucun binaire annexe. kdialog ou zenity ne servent plus que de repli, sur une
// session où le portail n'est pas joignable.
//
// Le dialogue se déroule en deux temps : l'appel rend un « handle » (un objet
// Request), et le portail émet le signal Response dessus quand l'utilisateur a
// fini. On s'abonne donc au signal *avant* l'appel, en devinant le chemin du
// handle à partir de notre nom unique sur le bus — c'est la convention décrite
// par la spécification, et c'est ce qui évite de rater une réponse immédiate.

const (
	portalBus  = "org.freedesktop.portal.Desktop"
	portalPath = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalIfc  = "org.freedesktop.portal.FileChooser"
	requestIfc = "org.freedesktop.portal.Request"

	// Une demande laissée ouverte ne doit pas immobiliser la goroutine pour
	// toujours si le portail disparaît sans jamais répondre.
	portalTimeout = time.Hour
)

// portalPattern décrit un motif ; la spécification attend le couple (type, motif)
// où 0 désigne un glob et 1 un type MIME.
type portalPattern struct {
	Kind    uint32
	Pattern string
}

// portalFilter est un filtre sérialisé, de signature « (sa(us)) ».
type portalFilter struct {
	Name     string
	Patterns []portalPattern
}

// portalOpen ouvre le dialogue du bureau. dir demande un dossier plutôt qu'un
// fichier ; filters est ignoré dans ce cas, la spécification ne le prévoyant que
// pour les fichiers.
func portalOpen(title string, dir bool, filters []Filter) (string, error) {
	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return "", errPortalUnavailable
	}
	defer conn.Close()
	// SessionBusPrivate rend une connexion brute : il faut s'authentifier et se
	// présenter avant de pouvoir appeler quoi que ce soit.
	if err := conn.Auth(nil); err != nil {
		return "", errPortalUnavailable
	}
	if err := conn.Hello(); err != nil {
		return "", errPortalUnavailable
	}

	token, err := handleToken()
	if err != nil {
		return "", err
	}
	expected := handlePath(conn.Names()[0], token)

	// Abonnement avant l'appel : le portail peut répondre sans délai, et le
	// signal serait alors perdu. On filtre sur le chemin plutôt que de le fixer
	// dans la règle, pour rester à l'écoute si le portail rend un autre handle.
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(requestIfc),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return "", errPortalUnavailable
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)

	options := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"modal":        dbus.MakeVariant(true),
		"multiple":     dbus.MakeVariant(false),
	}
	if dir {
		options["directory"] = dbus.MakeVariant(true)
	} else if len(filters) > 0 {
		options["filters"] = dbus.MakeVariant(toPortalFilters(filters))
	}

	var handle dbus.ObjectPath
	call := conn.Object(portalBus, portalPath).Call(portalIfc+".OpenFile", 0, "", title, options)
	switch err := call.Err.(type) {
	case nil:
	case *dbus.Error:
		// Pas de portail joignable, ni démarré ni activable : c'est le seul cas
		// où l'on retombe sur kdialog ou zenity.
		if err.Name == "org.freedesktop.DBus.Error.ServiceUnknown" ||
			err.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" ||
			err.Name == "org.freedesktop.DBus.Error.NotSupported" {
			return "", errPortalUnavailable
		}
		return "", err
	default:
		return "", err
	}
	if err := call.Store(&handle); err != nil {
		return "", err
	}

	deadline := time.After(portalTimeout)
	for {
		select {
		case sig, ok := <-signals:
			if !ok {
				return "", errPortalUnavailable
			}
			if sig.Name != requestIfc+".Response" || (sig.Path != handle && sig.Path != expected) {
				continue
			}
			return portalResult(sig.Body)
		case <-deadline:
			return "", fmt.Errorf("le portail n'a pas répondu")
		}
	}
}

// portalResult interprète le signal Response : (u response, a{sv} results).
func portalResult(body []any) (string, error) {
	if len(body) != 2 {
		return "", fmt.Errorf("réponse inattendue du portail")
	}
	code, ok := body[0].(uint32)
	if !ok {
		return "", fmt.Errorf("réponse inattendue du portail")
	}
	switch code {
	case 1:
		return "", zenity.ErrCanceled // l'utilisateur a fermé le dialogue
	case 2:
		return "", fmt.Errorf("le portail a interrompu la demande")
	}

	results, ok := body[1].(map[string]dbus.Variant)
	if !ok {
		return "", fmt.Errorf("réponse inattendue du portail")
	}
	var uris []string
	if err := results["uris"].Store(&uris); err != nil || len(uris) == 0 {
		return "", zenity.ErrCanceled // succès sans fichier : rien à retenir
	}
	return uriToPath(uris[0])
}

// uriToPath défait l'encodage d'une URI « file:// » rendue par le portail.
// Les espaces et les accents y arrivent échappés en pourcent.
func uriToPath(uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("chemin illisible renvoyé par le portail : %w", err)
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("chemin non local renvoyé par le portail : %s", uri)
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", fmt.Errorf("chemin illisible renvoyé par le portail : %w", err)
	}
	return path, nil
}

// handlePath reconstitue le chemin du Request, selon la convention du portail :
// « :1.42 » devient « 1_42 », et le jeton termine le chemin.
func handlePath(uniqueName, token string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
}

// handleToken fabrique un jeton unique. Il doit être imprévisible pour ne pas
// entrer en collision avec un autre appelant, et n'utiliser que des caractères
// valides dans un chemin d'objet.
func handleToken() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("jeton aléatoire indisponible : %w", err)
	}
	return "mysql_schema_diagram_" + hex.EncodeToString(buf[:]), nil
}

// toPortalFilters traduit nos filtres vers la forme attendue par le portail.
// Chaque motif est un glob : c'est ce que le sélecteur affiche à l'utilisateur.
func toPortalFilters(filters []Filter) []portalFilter {
	converted := make([]portalFilter, 0, len(filters))
	for _, f := range filters {
		patterns := make([]portalPattern, 0, len(f.Patterns))
		for _, p := range f.Patterns {
			patterns = append(patterns, portalPattern{Kind: 0, Pattern: p})
		}
		converted = append(converted, portalFilter{Name: f.Label, Patterns: patterns})
	}
	return converted
}
