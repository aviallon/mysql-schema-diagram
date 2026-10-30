// Command mysql-schema-diagram transforme un dump SQL de schéma MySQL ou MariaDB en
// schéma physique, sans aucun serveur de base de données.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/aviallon/mysql-schema-diagram/internal/app"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
)

// Ces valeurs sont injectées à la compilation (voir .goreleaser.yaml et Makefile).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("mysql-schema-diagram", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usage())
	}

	var (
		input        = fs.String("input", "", "fichier SQL à analyser (obligatoire)")
		output       = fs.String("output", ".", "dossier où écrire les livrables")
		serverVer    = fs.String("dialect", dialect.DefaultID, "version du serveur visée (voir -list-dialects)")
		listDialects = fs.Bool("list-dialects", false, "afficher les versions de serveur acceptées, puis quitter")
		showVersion  = fs.Bool("version", false, "afficher la version du programme, puis quitter")
		skipHTML     = fs.Bool("no-html", false, "ne pas écrire le rapport HTML")
		skipJSON     = fs.Bool("no-json", false, "ne pas écrire le catalogue JSON")
		skipSVG      = fs.Bool("no-svg", false, "ne pas écrire les diagrammes SVG")
		quiet        = fs.Bool("quiet", false, "ne rien afficher sauf les erreurs")
	)
	fs.StringVar(input, "i", "", "alias de -input")
	fs.StringVar(output, "o", ".", "alias de -output")

	if err := fs.Parse(args); err != nil {
		// -h et -help ne sont pas des erreurs : le message d'aide a déjà été
		// affiché, le programme doit se terminer avec un code de succès.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Printf("mysql-schema-diagram %s (commit %s, %s)\n", version, commit, date)
		return nil
	}
	if *listDialects {
		for _, d := range dialect.All() {
			fmt.Printf("%-14s %s\n", d.ID, d.Label)
		}
		return nil
	}
	if *input == "" {
		fs.Usage()
		return fmt.Errorf("le paramètre -input est obligatoire")
	}

	logf := func(format string, args ...any) {
		if !*quiet {
			fmt.Printf(format+"\n", args...)
		}
	}

	result, err := app.Run(app.Options{
		Input:    *input,
		Output:   *output,
		Version:  *serverVer,
		SkipHTML: *skipHTML,
		SkipJSON: *skipJSON,
		SkipSVG:  *skipSVG,
	}, logf)
	if err != nil {
		return err
	}
	logf("Écrit dans %s : %s", *output, strings.Join(result.Files, ", "))
	return nil
}

func usage() string {
	var b strings.Builder
	b.WriteString("mysql-schema-diagram — schéma physique depuis un dump SQL MySQL/MariaDB\n\n")
	b.WriteString("Usage :\n  mysql-schema-diagram -input dump.sql -output rapport/ [-dialect mysql-5.7]\n\n")
	b.WriteString("Le dump n'est jamais envoyé à un serveur : l'analyse est locale.\n\n")
	b.WriteString("Versions de serveur acceptées :\n")
	for _, d := range dialect.All() {
		fmt.Fprintf(&b, "  %-14s %s\n", d.ID, d.Label)
	}
	return b.String()
}
