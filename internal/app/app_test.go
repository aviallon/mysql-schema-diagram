package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aviallon/mysql-schema-diagram/internal/app"
	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
)

// La fixture du dépôt est synthétique : aucun schéma de base réelle n'est publié.
const fixture = "../../testdata/oracle/edge_cases.sql"

func TestRunProducesEveryAdvertisedFile(t *testing.T) {
	dir := t.TempDir()
	res, err := app.Run(app.Options{
		Input:   fixture,
		Output:  dir,
		Version: "mysql-5.7",
		Now:     func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) },
	}, nil)
	if err != nil {
		t.Fatalf("exécution : %v", err)
	}
	if len(res.Files) == 0 {
		t.Fatal("aucun fichier annoncé")
	}
	for _, name := range res.Files {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("fichier annoncé mais absent : %s (%v)", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("fichier annoncé mais vide : %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, app.FileJSON)); err != nil {
		t.Errorf("schema.json manquant : %v", err)
	}
}

func TestRunProducesAReadableCatalog(t *testing.T) {
	dir := t.TempDir()
	if _, err := app.Run(app.Options{Input: fixture, Output: dir, Version: "mysql-5.7"}, nil); err != nil {
		t.Fatalf("exécution : %v", err)
	}
	payload, err := os.ReadFile(filepath.Join(dir, app.FileJSON))
	if err != nil {
		t.Fatalf("lecture du catalogue : %v", err)
	}
	var got catalog.Catalog
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("le catalogue n'est pas un JSON valide : %v", err)
	}
	if len(got.Tables) == 0 {
		t.Fatal("le catalogue ne contient aucune table")
	}
	if got.Dialect != "mysql-5.7" {
		t.Errorf("dialecte consigné %q, attendu mysql-5.7", got.Dialect)
	}
	stats := got.Stats()
	if stats.Columns == 0 || stats.Indexes == 0 {
		t.Errorf("catalogue incohérent : %+v", stats)
	}

	html, err := os.ReadFile(filepath.Join(dir, app.FileHTML))
	if err != nil {
		t.Fatalf("lecture du rapport : %v", err)
	}
	for _, tb := range got.Tables {
		if !strings.Contains(string(html), "id='tbl-"+tb.Name+"'") {
			t.Errorf("la table %s manque au rapport HTML", tb.Name)
		}
	}
}

// L'option de version doit changer le résultat produit, pas seulement l'en-tête.
func TestRunDialectChangesTheResult(t *testing.T) {
	dir57, dir80 := t.TempDir(), t.TempDir()
	if _, err := app.Run(app.Options{Input: fixture, Output: dir57, Version: "mysql-5.7"}, nil); err != nil {
		t.Fatalf("exécution 5.7 : %v", err)
	}
	if _, err := app.Run(app.Options{Input: fixture, Output: dir80, Version: "mysql-8.0"}, nil); err != nil {
		t.Fatalf("exécution 8.0 : %v", err)
	}
	read := func(dir string) string {
		b, err := os.ReadFile(filepath.Join(dir, app.FileJSON))
		if err != nil {
			t.Fatalf("lecture : %v", err)
		}
		return string(b)
	}
	if read(dir57) == read(dir80) {
		t.Error("le catalogue est identique en 5.7 et en 8.0 : l'option de version est décorative")
	}
}

func TestRunRejectsUnknownDialect(t *testing.T) {
	if _, err := app.Run(app.Options{Input: fixture, Output: t.TempDir(), Version: "oracle-19"}, nil); err == nil {
		t.Error("un dialecte inconnu doit être refusé")
	}
}

func TestRunRejectsMissingInput(t *testing.T) {
	if _, err := app.Run(app.Options{Output: t.TempDir()}, nil); err == nil {
		t.Error("une entrée manquante doit être refusée")
	}
}

// Un avertissement dupliqué donne l'impression d'un problème plus grave qu'il
// n'est : la liste affichée doit être strictement identique à celle du catalogue.
func TestWarningsAreNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	fixture := "../../testdata/oracle/edge_cases.sql"
	res, err := app.Run(app.Options{Input: fixture, Output: dir, Version: "mysql-5.7"}, nil)
	if err != nil {
		t.Fatalf("exécution : %v", err)
	}
	if len(res.Catalog.Warnings) == 0 {
		t.Fatal("la fixture contient une contrainte CHECK ignorée par MySQL 5.7 : au moins un avertissement est attendu")
	}
	seen := map[string]int{}
	for _, w := range res.Catalog.Warnings {
		seen[w]++
	}
	for w, n := range seen {
		if n > 1 {
			t.Errorf("avertissement répété %d fois : %q", n, w)
		}
	}

	payload, err := os.ReadFile(filepath.Join(dir, app.FileJSON))
	if err != nil {
		t.Fatalf("lecture du catalogue : %v", err)
	}
	var got catalog.Catalog
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("catalogue illisible : %v", err)
	}
	if len(got.Warnings) != len(res.Catalog.Warnings) {
		t.Errorf("%d avertissements écrits contre %d dans le résultat",
			len(got.Warnings), len(res.Catalog.Warnings))
	}
}
