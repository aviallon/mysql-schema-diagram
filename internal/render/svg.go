// Package render produit les sorties visuelles : un diagramme SVG en grille
// déterministe, et un rapport HTML autonome.
//
// Le placement est volontairement calculé à la main plutôt que confié à un
// moteur de graphes : un schéma sans clé étrangère est un ensemble de tables
// sans arêtes, et Graphviz y produit une bande d'une seule colonne
// (mesuré : 515 × 32767 px, tronquée par la limite PNG).
package render

import (
	"fmt"
	"math"
	"strings"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
)

// GridOptions règle la mise en page du diagramme.
type GridOptions struct {
	// TargetWidth est la largeur visée, en pixels ; le nombre de colonnes en découle.
	TargetWidth float64
	// MaxColumns borne le nombre de colonnes.
	MaxColumns int
	// CardWidth fixe la largeur des cartes si non nulle.
	CardWidth float64
	// LinkTables entoure chaque carte d'un lien vers l'ancre #tbl-<nom>.
	LinkTables bool
	// Relations, si non vide, trace les liens entre les cartes concernées.
	Relations []catalog.Relation
}

// DefaultGridOptions renvoie la mise en page par défaut.
func DefaultGridOptions() GridOptions {
	return GridOptions{TargetWidth: 2400, MaxColumns: 8}
}

const (
	gridMargin   = 16.0
	gridGapX     = 20.0
	gridGapY     = 20.0
	headerHeight = 28.0
	rowHeight    = 17.0
	cardTail     = 10.0
	// charWidth estime la largeur d'un caractère monospace de 11 px.
	charWidth = 6.7
	// markerWidth réserve la place du marqueur de clé (PK/UQ/IX).
	markerWidth = 22.0
)

type card struct {
	table  *catalog.Table
	x      float64
	y      float64
	width  float64
	height float64
}

type grid struct {
	cards  []card
	width  float64
	height float64
}

// GridSVG rend un diagramme en grille des tables fournies.
func GridSVG(tables []*catalog.Table, opt GridOptions) string {
	if len(tables) == 0 {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="40"><text x="10" y="24" font-family="sans-serif" font-size="13">Aucune table</text></svg>`
	}
	width := opt.CardWidth
	if width <= 0 {
		width = cardWidth(tables)
	}
	g := place(tables, width, gridColumns(len(tables), width, opt))

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" class="sf-diagram">`,
		g.width, g.height, g.width, g.height)
	b.WriteString(`<style>
text { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 11px; fill: #1c1c1c; }
.sf-card rect { fill: #ffffff; stroke: #c9ccd4; stroke-width: 1; rx: 4; }
.sf-name { font-weight: 700; font-size: 11.5px; }
.sf-type { fill: #6b7280; }
.sf-flag { fill: #a8326b; font-weight: 700; }
.sf-bg { fill: #ffffff; }
.sf-rel { stroke: #a8326b; stroke-width: 1.2; fill: none; opacity: 0.42; }
.sf-sep { stroke: #e3e5ea; stroke-width: 1; }
</style>`)
	fmt.Fprintf(&b, `<rect class="sf-bg" x="0" y="0" width="%.0f" height="%.0f"/>`, g.width, g.height)

	if len(opt.Relations) > 0 {
		writeRelations(&b, g.cards, opt.Relations)
	}
	for _, c := range g.cards {
		writeCard(&b, c, opt)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// gridColumns choisit le nombre de colonnes pour respecter la largeur visée.
func gridColumns(count int, width float64, opt GridOptions) int {
	maxCols := opt.MaxColumns
	if maxCols <= 0 {
		maxCols = 8
	}
	target := opt.TargetWidth
	if target <= 0 {
		target = 2400
	}
	n := int(math.Round(target / (width + gridGapX)))
	if n < 1 {
		n = 1
	}
	if n > maxCols {
		n = maxCols
	}
	if n > count {
		n = count
	}
	return n
}

// cardWidth calcule une largeur unique qui accueille le nom le plus long et son type.
func cardWidth(tables []*catalog.Table) float64 {
	longestName, longestType := 8, 8
	for _, t := range tables {
		if len(t.Name) > longestName {
			longestName = len(t.Name)
		}
		for _, c := range t.Columns {
			if len(c.Name) > longestName {
				longestName = len(c.Name)
			}
			if len(c.Type) > longestType {
				longestType = len(c.Type)
			}
		}
	}
	if longestType > 18 {
		longestType = 18
	}
	w := gridMargin + markerWidth + float64(longestName)*charWidth + 14 + float64(longestType)*charWidth + 14
	if w < 250 {
		w = 250
	}
	if w > 460 {
		w = 460
	}
	return w
}

// place dispose les tables dans la colonne la plus courte (« masonry »).
//
// Une grille ligne par ligne est plus simple, mais réserve à chaque ligne la
// hauteur de sa plus grande carte : avec un grand schéma dont certaines
// en comptent 45 colonnes, cela creusait des trous de plusieurs centaines de
// pixels entre deux rangées. Ici la hauteur totale est celle de la colonne la
// plus chargée, sans trou.
func place(tables []*catalog.Table, width float64, columns int) grid {
	columnHeights := make([]float64, columns)
	for i := range columnHeights {
		columnHeights[i] = gridMargin
	}
	g := grid{}
	for _, t := range tables {
		col := shortestColumn(columnHeights)
		h := cardHeight(t)
		g.cards = append(g.cards, card{
			table:  t,
			x:      gridMargin + float64(col)*(width+gridGapX),
			y:      columnHeights[col],
			width:  width,
			height: h,
		})
		columnHeights[col] += h + gridGapY
	}
	tallest := 0.0
	for _, h := range columnHeights {
		if h > tallest {
			tallest = h
		}
	}
	g.width = gridMargin*2 + float64(columns)*(width+gridGapX) - gridGapX
	g.height = tallest - gridGapY + gridMargin
	return g
}

// shortestColumn renvoie l'indice de la colonne la moins haute ; en cas d'égalité
// la plus à gauche, pour que le placement reste déterministe.
func shortestColumn(heights []float64) int {
	best := 0
	for i, h := range heights {
		if h < heights[best] {
			best = i
		}
	}
	return best
}

func cardHeight(t *catalog.Table) float64 {
	return headerHeight + float64(len(t.Columns))*rowHeight + cardTail
}

func writeCard(b *strings.Builder, c card, opt GridOptions) {
	if opt.LinkTables {
		fmt.Fprintf(b, `<a xlink:href="#tbl-%s">`, escape(c.table.Name))
	}
	fmt.Fprintf(b, `<g class="sf-card"><rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" rx="4"/>`, c.x, c.y, c.width, c.height)
	fmt.Fprintf(b, `<text class="sf-name" x="%.0f" y="%.0f">%s</text>`,
		c.x+8, c.y+16, escape(truncate(c.table.Name, 34)))
	label := c.table.Engine
	if label == "" {
		label = "table"
	}
	fmt.Fprintf(b, `<text class="sf-type" x="%.0f" y="%.0f" text-anchor="end">[%s]</text>`,
		c.x+c.width-8, c.y+16, escape(label))
	fmt.Fprintf(b, `<line class="sf-sep" x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f"/>`,
		c.x+1, c.y+headerHeight-6, c.x+c.width-1, c.y+headerHeight-6)

	nameChars := charsFor(c.width - markerWidth - 38)
	typeChars := charsFor(c.width - markerWidth - 40)
	for i, col := range c.table.Columns {
		y := c.y + headerHeight + float64(i)*rowHeight + 12
		if flag := keyFlag(col); flag != "" {
			fmt.Fprintf(b, `<text class="sf-flag" x="%.0f" y="%.0f">%s</text>`, c.x+8, y, flag)
		}
		fmt.Fprintf(b, `<text x="%.0f" y="%.0f">%s</text>`, c.x+markerWidth, y, escape(truncate(col.Name, nameChars)))
		fmt.Fprintf(b, `<text class="sf-type" x="%.0f" y="%.0f" text-anchor="end">%s</text>`,
			c.x+c.width-8, y, escape(truncate(col.Type, typeChars)))
	}
	b.WriteString(`</g>`)
	if opt.LinkTables {
		b.WriteString(`</a>`)
	}
}

func keyFlag(c *catalog.Column) string {
	switch c.Key {
	case "PRI":
		return "PK"
	case "UNI":
		return "UQ"
	case "MUL":
		return "IX"
	}
	return ""
}

// writeRelations trace les liens sous les cartes, d'un centre à l'autre.
func writeRelations(b *strings.Builder, cards []card, relations []catalog.Relation) {
	byName := make(map[string]card, len(cards))
	for _, c := range cards {
		byName[strings.ToLower(c.table.Name)] = c
	}
	for _, rel := range relations {
		child, ok := byName[strings.ToLower(rel.ChildTable)]
		if !ok {
			continue
		}
		parent, ok := byName[strings.ToLower(rel.ParentTable)]
		if !ok {
			continue
		}
		x1, y1 := child.x+child.width/2, child.y+child.height/2
		x2, y2 := parent.x+parent.width/2, parent.y+parent.height/2
		// Courbe de Bézier verticale : plus lisible qu'un segment sur une grille.
		cy := (y1 + y2) / 2
		fmt.Fprintf(b, `<path class="sf-rel" d="M %.0f %.0f C %.0f %.0f, %.0f %.0f, %.0f %.0f"/>`,
			x1, y1, x1, cy, x2, cy, x2, y2)
	}
}

func charsFor(pixels float64) int {
	n := int(pixels / charWidth)
	if n < 6 {
		n = 6
	}
	return n
}

func truncate(s string, maxChars int) string {
	r := []rune(s)
	if maxChars <= 0 || len(r) <= maxChars {
		return s
	}
	if maxChars == 1 {
		return "…"
	}
	return string(r[:maxChars-1]) + "…"
}

var escaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

func escape(s string) string { return escaper.Replace(s) }
