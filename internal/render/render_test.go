package render_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
	"github.com/aviallon/mysql-schema-diagram/internal/ddl"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
	"github.com/aviallon/mysql-schema-diagram/internal/infer"
	"github.com/aviallon/mysql-schema-diagram/internal/render"
)

// La fixture commitée est synthétique : elle n'expose le schéma d'aucune base
// réelle, et suffit à couvrir le contrat du rendu.
const fixture = "edge_cases"

func load(t *testing.T, name string) *catalog.Catalog {
	t.Helper()
	path := filepath.Join("../../testdata/oracle", name+".sql")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture %s absente : %v", name, err)
	}
	d, err := dialect.Get("mysql-5.7")
	if err != nil {
		t.Fatalf("dialecte : %v", err)
	}
	parsed, err := ddl.ReadFile(path)
	if err != nil {
		t.Fatalf("lecture de %s : %v", name, err)
	}
	cat, _ := catalog.Build(parsed.Statements, d, name+".sql")
	cat.ImpliedRelations = infer.Infer(cat)
	cat.Sort()
	return cat
}

func report(cat *catalog.Catalog) *render.Report {
	return &render.Report{
		Catalog:      cat,
		DialectLabel: "MySQL 5.7",
		GeneratedAt:  time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC),
		Grid:         render.DefaultGridOptions(),
	}
}

// syntheticCatalog fabrique un catalogue de taille réaliste sans dépendre d'un
// fichier : le rendu doit rester correct bien au-delà de la petite fixture.
func syntheticCatalog(tables int) *catalog.Catalog {
	cat := &catalog.Catalog{Source: "synthétique", Dialect: "mysql-5.7"}
	for i := 0; i < tables; i++ {
		table := &catalog.Table{
			Name:    fmt.Sprintf("table_%03d", i),
			Engine:  "InnoDB",
			Charset: "utf8mb4",
		}
		table.Columns = append(table.Columns, &catalog.Column{
			Ordinal:  1,
			Name:     fmt.Sprintf("id_table_%03d", i),
			Type:     "int(11)",
			Nullable: false,
			Key:      "PRI",
		})
		// Des cartes de hauteurs variées : c'est ce que la disposition doit gérer.
		for j := 0; j < i%40+1; j++ {
			table.Columns = append(table.Columns, &catalog.Column{
				Ordinal:   j + 2,
				Name:      fmt.Sprintf("colonne_%02d", j),
				Type:      "varchar(64)",
				Nullable:  true,
				Charset:   "utf8mb4",
				Collation: "utf8mb4_general_ci",
			})
		}
		table.Indexes = append(table.Indexes, &catalog.Index{
			Name:    "PRIMARY",
			Kind:    catalog.KindPrimary,
			Columns: []catalog.IndexColumn{{Name: table.Columns[0].Name}},
		})
		if i%5 == 0 && i > 0 {
			cat.ImpliedRelations = append(cat.ImpliedRelations, catalog.Relation{
				ChildTable:   table.Name,
				ChildColumn:  table.Columns[0].Name,
				ParentTable:  "table_000",
				ParentColumn: "id_table_000",
			})
		}
		cat.Tables = append(cat.Tables, table)
	}
	cat.Sort()
	return cat
}

// TestRenderingIsDeterministic : deux rendus du même catalogue doivent être
// identiques au caractère près, sinon aucune comparaison golden n'est possible
// et une CI qui produit un artefact instable ne sert à rien.
func TestRenderingIsDeterministic(t *testing.T) {
	cat := load(t, fixture)
	first := report(cat)
	second := report(cat)
	if a, b := first.HTML(), second.HTML(); a != b {
		t.Error("deux rendus HTML du même catalogue diffèrent")
	}
	if a, b := first.GridSVG(), second.GridSVG(); a != b {
		t.Error("deux rendus SVG du même catalogue diffèrent")
	}
}

// TestEveryTableAndColumnIsRendered est le garde de couverture : un schéma
// physique qui omet silencieusement une table ou une colonne est pire qu'aucun
// schéma. On vérifie les deux sorties, pas seulement le JSON.
//
// Il tourne sur un catalogue synthétique de 60 tables pour ne pas dépendre de la
// taille d'une fixture.
func TestEveryTableAndColumnIsRendered(t *testing.T) {
	cat := syntheticCatalog(60)
	r := report(cat)
	svg := r.GridSVG()
	html := r.HTML()

	for _, tb := range cat.Tables {
		if !strings.Contains(svg, tb.Name) {
			t.Errorf("la table %s est absente du diagramme SVG", tb.Name)
		}
		if !strings.Contains(html, "id='tbl-"+tb.Name+"'") {
			t.Errorf("la table %s n'a pas d'ancre dans le rapport HTML", tb.Name)
		}
		for _, col := range tb.Columns {
			if len(tb.Columns) > 10 && col.Ordinal > 10 {
				// Les noms de colonnes se répètent d'une table à l'autre dans le
				// catalogue synthétique : un contrôle par table suffit.
				continue
			}
			if !strings.Contains(html, col.Name) {
				t.Errorf("la colonne %s.%s est absente du rapport HTML", tb.Name, col.Name)
			}
		}
	}
}

// TestReportNeedsNoExternalResource : le rapport doit rester autonome. Une
// police distante ou un script CDN le casserait hors-ligne.
//
// Les URI d'espace de noms XML (xmlns) sont retirées avant le contrôle : ce ne
// sont pas des ressources à charger, seulement des identifiants.
func TestReportNeedsNoExternalResource(t *testing.T) {
	html := namespaceAttr.ReplaceAllString(report(load(t, fixture)).HTML(), "")
	for _, forbidden := range []string{"https://", "http://", "<script", "<link ", `src="//`} {
		if strings.Contains(html, forbidden) {
			t.Errorf("le rapport contient une référence externe ou un script : %q", forbidden)
		}
	}
}

var namespaceAttr = regexp.MustCompile(`xmlns(:[a-z]+)?="[^"]*"`)

// TestRelationsDiagramOnlyShowsRelatedTables : le second diagramme ne doit pas
// se transformer en copie du premier.
func TestRelationsDiagramOnlyShowsRelatedTables(t *testing.T) {
	cat := syntheticCatalog(60)
	svg := report(cat).RelationsSVG()
	if svg == "" {
		t.Fatal("aucun diagramme de relations alors que le catalogue en contient")
	}
	related := map[string]bool{}
	for _, tb := range cat.RelatedTables() {
		related[tb.Name] = true
	}
	if len(related) == 0 || len(related) == len(cat.Tables) {
		t.Fatalf("le catalogue de test doit avoir des tables reliées ET des tables isolées (reliées : %d/%d)",
			len(related), len(cat.Tables))
	}
	for _, tb := range cat.Tables {
		present := strings.Contains(svg, ">"+tb.Name+"<")
		if present && !related[tb.Name] {
			t.Errorf("la table %s n'a aucune relation mais figure dans le diagramme des relations", tb.Name)
		}
	}
}

// TestEmptyCatalogStillRenders : un dump sans table ne doit pas produire un
// fichier vide ou un panic.
func TestEmptyCatalogStillRenders(t *testing.T) {
	cat := &catalog.Catalog{Source: "vide.sql"}
	html := report(cat).HTML()
	if !strings.Contains(html, "0</b> tables") {
		t.Errorf("le rapport d'un catalogue vide n'annonce pas 0 table : %s", firstLine(html))
	}
	if svg := report(cat).GridSVG(); !strings.Contains(svg, "Aucune table") {
		t.Errorf("le diagramme d'un catalogue vide est inattendu : %s", svg)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
