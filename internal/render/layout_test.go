package render

import (
	"fmt"
	"testing"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
)

// syntheticTables fabrique des tables de hauteurs très variées (1 à 60
// colonnes) : c'est le cas qui cassait la disposition ligne par ligne, où
// chaque rangée réservait la hauteur de sa plus grande carte.
func syntheticTables(count int) []*catalog.Table {
	tables := make([]*catalog.Table, 0, count)
	for i := 0; i < count; i++ {
		table := &catalog.Table{Name: fmt.Sprintf("t_%03d", i)}
		for j := 0; j < i%60+1; j++ {
			table.Columns = append(table.Columns, &catalog.Column{
				Ordinal: j + 1,
				Name:    fmt.Sprintf("c_%02d", j),
				Type:    "int(11)",
			})
		}
		tables = append(tables, table)
	}
	return tables
}

// TestCardsDoNotOverlap est le garde géométrique : une disposition « masonry »
// mal calculée empile deux tables l'une sur l'autre, et le diagramme devient
// faux sans qu'aucun test de contenu ne s'en aperçoive.
func TestCardsDoNotOverlap(t *testing.T) {
	tables := syntheticTables(80)
	for _, columns := range []int{1, 2, 3, 5, 8} {
		g := place(tables, 320, columns)
		for i := 0; i < len(g.cards); i++ {
			a := g.cards[i]
			if a.x < 0 || a.y < 0 || a.x+a.width > g.width || a.y+a.height > g.height {
				t.Errorf("columns=%d : la carte %s sort du cadre (%.0f,%.0f %.0fx%.0f dans %.0fx%.0f)",
					columns, a.table.Name, a.x, a.y, a.width, a.height, g.width, g.height)
			}
			for j := i + 1; j < len(g.cards); j++ {
				b := g.cards[j]
				if a.x < b.x+b.width && b.x < a.x+a.width && a.y < b.y+b.height && b.y < a.y+a.height {
					t.Errorf("columns=%d : %s et %s se chevauchent",
						columns, a.table.Name, b.table.Name)
				}
			}
		}
	}
}

// Toutes les tables doivent être placées exactement une fois.
func TestEveryTableIsPlacedOnce(t *testing.T) {
	tables := syntheticTables(40)
	g := place(tables, 320, 4)
	if len(g.cards) != len(tables) {
		t.Fatalf("%d cartes placées pour %d tables", len(g.cards), len(tables))
	}
	seen := map[string]bool{}
	for _, c := range g.cards {
		if seen[c.table.Name] {
			t.Errorf("la table %s est placée deux fois", c.table.Name)
		}
		seen[c.table.Name] = true
	}
}

// La disposition doit être déterministe : deux appels identiques donnent des
// coordonnées identiques.
func TestLayoutIsDeterministic(t *testing.T) {
	tables := syntheticTables(30)
	first := place(tables, 320, 5)
	second := place(tables, 320, 5)
	if first.width != second.width || first.height != second.height {
		t.Fatalf("dimensions instables : %.0fx%.0f puis %.0fx%.0f",
			first.width, first.height, second.width, second.height)
	}
	for i := range first.cards {
		if first.cards[i] != second.cards[i] {
			t.Fatalf("carte %d instable : %+v puis %+v", i, first.cards[i], second.cards[i])
		}
	}
}

// La disposition « masonry » doit tenir la promesse de son nom : avec des cartes
// de hauteurs très inégales, la hauteur totale doit rester proche de la hauteur
// de la colonne la plus chargée, pas de la somme des plus hautes cartes par rangée.
func TestMasonryUsesTheShortestColumn(t *testing.T) {
	tables := syntheticTables(80)
	g := place(tables, 320, 4)

	// Une disposition ligne par ligne donnerait au moins la hauteur cumulée des
	// « plus grandes cartes de chaque rangée » ; on vérifie qu'on reste en dessous.
	columns := 4
	rowMajorHeight := gridMargin
	for start := 0; start < len(tables); start += columns {
		end := start + columns
		if end > len(tables) {
			end = len(tables)
		}
		tallest := 0.0
		for _, table := range tables[start:end] {
			if h := cardHeight(table); h > tallest {
				tallest = h
			}
		}
		rowMajorHeight += tallest + gridGapY
	}
	if g.height >= rowMajorHeight {
		t.Errorf("disposition non compacte : %.0f px contre %.0f px en ligne par ligne", g.height, rowMajorHeight)
	}
}
