// Package app assemble le pipeline complet : lire un dump SQL, en déduire le
// catalogue, puis écrire les livrables.
//
// Rien ici n'ouvre de connexion réseau : l'analyse est entièrement hors-ligne.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
	"github.com/aviallon/mysql-schema-diagram/internal/ddl"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
	"github.com/aviallon/mysql-schema-diagram/internal/infer"
	"github.com/aviallon/mysql-schema-diagram/internal/render"
)

// Noms des fichiers produits.
const (
	FileJSON        = "schema.json"
	FileHTML        = "schema_physique.html"
	FileSVG         = "schema.svg"
	FileRelations   = "relations.svg"
	defaultDirPerm  = 0o755
	defaultFilePerm = 0o644
)

// Options décrit une exécution.
type Options struct {
	// Input est le fichier SQL à analyser.
	Input string
	// Output est le dossier où écrire les livrables.
	Output string
	// Version est l'identifiant de dialecte ("mysql-5.7", ...). Vide = défaut.
	Version string

	SkipJSON bool
	SkipHTML bool
	SkipSVG  bool

	// Now permet de fixer l'horodatage (tests).
	Now func() time.Time
}

// Result décrit ce qui a été produit.
type Result struct {
	Catalog *catalog.Catalog
	Dialect dialect.Dialect
	Files   []string
	// Output est le dossier où les livrables ont été écrits.
	Output string
}

// Logf reçoit les messages de progression. Une fonction vide est acceptée.
type Logf func(format string, args ...any)

// Run exécute le pipeline complet.
func Run(opt Options, logf Logf) (*Result, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opt.Input == "" {
		return nil, fmt.Errorf("aucun fichier d'entrée fourni")
	}
	if opt.Output == "" {
		return nil, fmt.Errorf("aucun dossier de sortie fourni")
	}
	version := opt.Version
	if version == "" {
		version = dialect.DefaultID
	}
	d, err := dialect.Get(version)
	if err != nil {
		return nil, err
	}

	logf("Lecture de %s", opt.Input)
	parsed, err := ddl.ReadFile(opt.Input)
	if err != nil {
		return nil, err
	}
	logf("%d instruction(s) SQL analysée(s)", len(parsed.Statements))

	cat, buildWarnings := catalog.Build(parsed.Statements, d, opt.Input)
	// Build a déjà posé ses propres avertissements dans cat.Warnings : on
	// reconstruit la liste à partir des deux sources pour ne pas les compter
	// deux fois (ce qui produisait un doublon par contrainte CHECK).
	cat.Warnings = append(append([]string{}, parsed.Warnings...), buildWarnings...)
	relations, inferWarnings := infer.InferWithWarnings(cat)
	cat.ImpliedRelations = relations
	cat.Warnings = append(cat.Warnings, inferWarnings...)
	cat.Sort()

	stats := cat.Stats()
	logf("%d tables, %d colonnes, %d index, %d relation(s) déduite(s)",
		stats.Tables, stats.Columns, stats.Indexes, stats.ImpliedFK)

	if err := os.MkdirAll(opt.Output, defaultDirPerm); err != nil {
		return nil, fmt.Errorf("création du dossier de sortie : %w", err)
	}

	now := time.Now()
	if opt.Now != nil {
		now = opt.Now()
	}
	report := &render.Report{
		Catalog:      cat,
		DialectLabel: d.Label,
		GeneratedAt:  now,
		Grid:         render.DefaultGridOptions(),
	}
	res := &Result{Catalog: cat, Dialect: d, Output: opt.Output}

	if !opt.SkipJSON {
		if err := writeJSON(filepath.Join(opt.Output, FileJSON), cat); err != nil {
			return nil, err
		}
		res.Files = append(res.Files, FileJSON)
	}
	if !opt.SkipHTML {
		name := filepath.Join(opt.Output, FileHTML)
		if err := writeFile(name, report.HTML()); err != nil {
			return nil, err
		}
		res.Files = append(res.Files, FileHTML)
	}
	if !opt.SkipSVG {
		if err := writeFile(filepath.Join(opt.Output, FileSVG), report.GridSVG()); err != nil {
			return nil, err
		}
		res.Files = append(res.Files, FileSVG)
		if rel := report.RelationsSVG(); rel != "" {
			if err := writeFile(filepath.Join(opt.Output, FileRelations), rel); err != nil {
				return nil, err
			}
			res.Files = append(res.Files, FileRelations)
		}
	}

	for _, w := range cat.Warnings {
		logf("avertissement : %s", w)
	}
	return res, nil
}

func writeJSON(path string, cat *catalog.Catalog) error {
	payload, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("sérialisation JSON : %w", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, defaultFilePerm); err != nil {
		return fmt.Errorf("écriture de %s : %w", path, err)
	}
	return nil
}

func writeFile(path, content string) error {
	if err := os.WriteFile(path, []byte(content), defaultFilePerm); err != nil {
		return fmt.Errorf("écriture de %s : %w", path, err)
	}
	return nil
}
