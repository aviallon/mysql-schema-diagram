package main

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/aviallon/mysql-schema-diagram/internal/app"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
	"github.com/aviallon/mysql-schema-diagram/internal/nativedialog"
)

// ui porte tout l'état de la fenêtre. Gio étant en mode immédiat, il n'y a pas
// d'arbre de widgets : cette structure joue le rôle de modèle.
//
// Gio n'autorise la modification de l'état que depuis la goroutine principale.
// L'analyse tourne donc dans une goroutine qui n'écrit jamais dans ui : elle
// dépose des événements dans un canal, que layout() consomme.
type ui struct {
	th *material.Theme

	input   widget.Editor
	output  widget.Editor
	version widget.Enum

	browseInput  widget.Clickable
	browseOutput widget.Clickable
	generate     widget.Clickable

	log      widget.List
	logLines []string

	status    string
	statusErr bool
	running   bool
	lastDir   string

	events     chan workEvent
	dialogs    chan dialogResult
	invalidate func()
}

// dialogResult est le retour du dialogue système, ramené sur la goroutine
// principale : Gio n'autorise l'écriture de l'état que là.
type dialogResult struct {
	path  string
	isDir bool
	err   error
}

// colorDanger est le rouge du message d'erreur.
var colorDanger = color.NRGBA{R: 0xb2, G: 0x33, B: 0x33, A: 0xff}

// workEvent est ce qu'une analyse en cours peut rapporter à l'interface.
type workEvent struct {
	line string
	done *app.Result
	err  error
}

// themeWithoutSystemFonts embarque la police Go au lieu de compter sur les
// polices installées. Un préfixe Wine de nixpkgs n'en fournit aucune : sans cela
// la fenêtre s'ouvre, mais sans le moindre texte.
func themeWithoutSystemFonts() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Regular()))
	return th
}

func newUI(invalidate func()) *ui {
	u := &ui{
		th:         themeWithoutSystemFonts(),
		status:     "Choisissez un fichier SQL, un dossier de sortie, puis lancez l'analyse.",
		log:        widget.List{List: layout.List{Axis: layout.Vertical}},
		events:     make(chan workEvent, 256),
		dialogs:    make(chan dialogResult, 4),
		invalidate: invalidate,
	}
	u.input.SingleLine = true
	u.output.SingleLine = true
	u.version.Value = dialect.DefaultID
	return u
}

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	u.drain()

	u.handleClicks(gtx)

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.form(gtx) }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return u.actions(gtx) }),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.journal(gtx) }),
	)
}

// drain applique les événements de la goroutine de travail. Appelé depuis la
// goroutine principale uniquement.
func (u *ui) drain() {
	for {
		select {
		case res := <-u.dialogs:
			u.applyDialog(res)
		case ev := <-u.events:
			switch {
			case ev.err != nil:
				u.running = false
				u.setStatus("Échec : "+ev.err.Error(), true)
				u.appendLog("erreur : " + ev.err.Error())
			case ev.done != nil:
				u.running = false
				stats := ev.done.Catalog.Stats()
				u.lastDir = ev.done.Output
				u.setStatus(fmt.Sprintf(
					"%d tables, %d colonnes, %d relations déduites — écrit dans %s",
					stats.Tables, stats.Columns, stats.ImpliedFK, ev.done.Output), false)
			default:
				u.appendLog(ev.line)
			}
		default:
			return
		}
	}
}

func (u *ui) handleClicks(gtx layout.Context) {
	if u.browseInput.Clicked(gtx) {
		u.chooseFile()
	}
	if u.browseOutput.Clicked(gtx) {
		u.chooseFolder()
	}
	if u.generate.Clicked(gtx) && !u.running {
		u.start()
	}
	if u.version.Update(gtx) {
		u.setStatus("Version du serveur visée : "+u.version.Value, false)
	}
}

func (u *ui) form(gtx layout.Context) layout.Dimensions {
	label := func(text string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(12), Bottom: unit.Dp(2)}.Layout(gtx,
				material.Body2(u.th, text).Layout)
		}
	}
	field := func(ed *widget.Editor, browse *widget.Clickable, hint string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return material.Editor(u.th, ed, hint).Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return material.Button(u.th, browse, "Parcourir…").Layout(gtx)
				}),
			)
		}
	}
	versions := func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(dialect.All()))
		for _, d := range dialect.All() {
			d := d
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return u.version.Layout(gtx, d.ID, func(gtx layout.Context) layout.Dimensions {
					return material.RadioButton(u.th, &u.version, d.ID, d.Label).Layout(gtx)
				})
			}))
		}
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
	}

	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16), Top: unit.Dp(4)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(label("Fichier SQL (export de structure, sans données)")),
				layout.Rigid(field(&u.input, &u.browseInput, "chemin du dump .sql")),
				layout.Rigid(label("Dossier de sortie")),
				layout.Rigid(field(&u.output, &u.browseOutput, "dossier où écrire le schéma")),
				layout.Rigid(label("Version du serveur visée")),
				layout.Rigid(versions),
			)
		})
}

func (u *ui) actions(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					text := "Générer le schéma"
					if u.running {
						text = "Analyse en cours…"
					}
					return material.Button(u.th, &u.generate, text).Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(12)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						style := material.Body2(u.th, u.status)
						if u.statusErr {
							style.Color = colorDanger
						}
						return style.Layout(gtx)
					})
				}),
			)
		})
}

func (u *ui) journal(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Left: unit.Dp(16), Right: unit.Dp(16)}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			if len(u.logLines) == 0 {
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}
			return u.log.Layout(gtx, len(u.logLines), func(gtx layout.Context, i int) layout.Dimensions {
				return material.Body2(u.th, u.logLines[i]).Layout(gtx)
			})
		})
}

func (u *ui) appendLog(line string) {
	u.logLines = append(u.logLines, line)
	u.log.ScrollToEnd = true
}

func (u *ui) setStatus(text string, isError bool) {
	u.status = text
	u.statusErr = isError
}

// start lance l'analyse hors de la goroutine principale. La goroutine de travail
// ne touche à aucun champ de ui : elle n'écrit que dans le canal.
func (u *ui) start() {
	if strings.TrimSpace(u.input.Text()) == "" {
		u.setStatus("Le fichier SQL est obligatoire.", true)
		return
	}
	if strings.TrimSpace(u.output.Text()) == "" {
		u.setStatus("Le dossier de sortie est obligatoire.", true)
		return
	}
	version := u.version.Value
	if version == "" {
		version = dialect.DefaultID
	}
	opts := app.Options{
		Input:   u.input.Text(),
		Output:  u.output.Text(),
		Version: version,
	}
	u.running = true
	u.statusErr = false
	u.setStatus("Analyse en cours…", false)
	u.logLines = nil

	events := u.events
	invalidate := u.invalidate
	go func() {
		// Un panic dans l'analyse ne doit pas tuer la fenêtre sans un mot : on le
		// transforme en erreur affichée, comme n'importe quel autre échec.
		defer func() {
			if r := recover(); r != nil {
				events <- workEvent{err: fmt.Errorf("panique pendant l'analyse : %v", r)}
				invalidate()
			}
		}()

		// Le canal ne doit jamais bloquer la goroutine : si l'interface n'a pas
		// encore drainé (fenêtre occluse, compositeur qui ne délivre plus de
		// frames), l'analyse doit continuer et écrire ses livrables. On plafonne
		// donc le journal et on signale ce qui n'a pas pu être affiché.
		const maxLogLines = 500
		sent, skipped := 0, 0
		logf := func(format string, args ...any) {
			if sent >= maxLogLines {
				skipped++
				return
			}
			sent++
			select {
			case events <- workEvent{line: fmt.Sprintf(format, args...)}:
			default:
				skipped++
			}
			invalidate()
		}

		res, err := app.Run(opts, logf)
		if skipped > 0 {
			select {
			case events <- workEvent{line: fmt.Sprintf("… %d ligne(s) de journal non affichée(s)", skipped)}:
			default:
			}
		}
		if err != nil {
			events <- workEvent{err: err}
			invalidate()
			return
		}
		res.Output = opts.Output
		events <- workEvent{done: res}
		invalidate()
	}()
}

// chooseFile ouvre le dialogue du système dans une goroutine : il bloque jusqu'au
// choix de l'utilisateur, et Gio n'admet pas qu'on bloque la boucle d'événements.
// Le résultat revient par un canal, consommé par drain().
func (u *ui) chooseFile() {
	go func() {
		path, err := nativedialog.OpenFile("Choisir le dump SQL",
			nativedialog.Filter{Label: "Fichiers SQL", Patterns: []string{"*.sql"}},
			nativedialog.Filter{Label: "Tous les fichiers", Patterns: []string{"*"}})
		u.dialogs <- dialogResult{path: path, err: err}
		u.invalidate()
	}()
}

func (u *ui) chooseFolder() {
	go func() {
		path, err := nativedialog.OpenFolder("Choisir le dossier de sortie")
		u.dialogs <- dialogResult{path: path, isDir: true, err: err}
		u.invalidate()
	}()
}

// applyDialog applique le choix de l'utilisateur, sur la goroutine principale.
func (u *ui) applyDialog(res dialogResult) {
	switch {
	case res.err != nil:
		u.setStatus(res.err.Error(), true)
	case res.path == "":
		// Annulation : il n'y a rien à signaler.
	case res.isDir:
		u.output.SetText(res.path)
	default:
		u.input.SetText(res.path)
		if strings.TrimSpace(u.output.Text()) == "" {
			u.output.SetText(filepath.Dir(res.path))
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
