// Command mysql-schema-diagram-gui propose la même analyse que mysql-schema-diagram derrière une
// fenêtre à trois champs : fichier SQL, dossier de sortie, version du serveur.
//
// Gio est retenu pour une raison précise : sur Windows il se compile avec
// CGO_ENABLED=0 et produit un exécutable unique qui n'importe AUCUNE DLL, pas
// même celles de mingw-w64 (vérifié avec objdump). Fyne, lui, impose cgo et une
// chaîne mingw à l'édition de liens.
package main

import (
	"fmt"
	"os"

	gioapp "gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"
)

// version est injectée à la compilation (voir Makefile et CI).
var version = "dev"

func main() {
	go func() {
		// Gio panique si la fenêtre ne peut pas être créée (pas d'affichage, pas
		// de pilote graphique utilisable). Sans ce filet, l'utilisateur reçoit
		// une trace de pile au lieu d'une explication et d'une alternative.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Impossible d'ouvrir la fenêtre : %v\n", r)
				fmt.Fprintln(os.Stderr,
					"Sur une machine sans affichage, utilisez le binaire « mysql-schema-diagram » en ligne de commande.")
				os.Exit(2)
			}
		}()

		w := new(gioapp.Window)
		w.Option(
			gioapp.Title(fmt.Sprintf("MySQL Schema Diagram %s — schéma physique depuis un dump SQL", version)),
			gioapp.Size(unit.Dp(940), unit.Dp(700)),
			gioapp.MinSize(unit.Dp(640), unit.Dp(480)),
		)
		ui := newUI(w.Invalidate)
		var ops op.Ops
		for {
			switch e := w.Event().(type) {
			case gioapp.DestroyEvent:
				os.Exit(0)
			case gioapp.FrameEvent:
				gtx := gioapp.NewContext(&ops, e)
				ui.layout(gtx)
				e.Frame(gtx.Ops)
			}
		}
	}()
	gioapp.Main()
}
