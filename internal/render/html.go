package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
)

// Report assemble un rapport HTML autonome : aucune ressource externe, les
// diagrammes sont inclus en SVG.
type Report struct {
	Catalog      *catalog.Catalog
	DialectLabel string
	GeneratedAt  time.Time
	Grid         GridOptions
}

// HTML rend le rapport complet.
func (r *Report) HTML() string {
	cat := r.Catalog
	stats := cat.Stats()
	title := "Schéma physique"
	if base := baseName(cat.Source); base != "" {
		title = "Schéma physique — base " + base
	}

	gridOpts := r.Grid
	gridOpts.LinkTables = true
	gridAll := GridSVG(cat.Tables, gridOpts)

	related := cat.RelatedTables()
	relGridOpts := r.Grid
	relGridOpts.LinkTables = true
	relGridOpts.Relations = cat.AllRelations()
	gridRelations := ""
	if len(related) > 0 && len(cat.AllRelations()) > 0 {
		gridRelations = GridSVG(related, relGridOpts)
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html lang='fr'><head><meta charset='utf-8'>")
	b.WriteString("<meta name='viewport' content='width=device-width,initial-scale=1'>")
	fmt.Fprintf(&b, "<title>%s</title>", escape(title))
	fmt.Fprintf(&b, "<style>%s</style></head><body>", reportCSS)

	b.WriteString("<header>")
	fmt.Fprintf(&b, "<h1>%s</h1>", escape(title))
	fmt.Fprintf(&b, "<div class='stats'><b>%d</b> tables &middot; <b>%d</b> colonnes &middot; <b>%d</b> index",
		stats.Tables, stats.Columns, stats.Indexes)
	if stats.Views > 0 {
		fmt.Fprintf(&b, " &middot; <b>%d</b> vues", stats.Views)
	}
	fmt.Fprintf(&b, " &middot; <b>%d</b> clés étrangères déclarées &middot; <b>%d</b> relations déduites",
		stats.DeclaredFK, stats.ImpliedFK)
	if stats.TablesWithoutPK > 0 {
		fmt.Fprintf(&b, " &middot; <b>%d</b> tables sans clé primaire", stats.TablesWithoutPK)
	}
	b.WriteString("<br>")
	fmt.Fprintf(&b, "Analysé hors-ligne depuis <code>%s</code>, interprété comme %s",
		escape(cat.Source), escape(r.DialectLabel))
	if !r.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, " &middot; généré le %s", escape(r.GeneratedAt.Format("02/01/2006 15:04")))
	}
	b.WriteString("</div></header><main>")

	if stats.DeclaredFK == 0 && stats.Tables > 0 {
		b.WriteString("<div class='note'><b>Aucune contrainte <code>FOREIGN KEY</code> dans ce dump.</b> ")
		fmt.Fprintf(&b, "Le schéma déclaré est un ensemble de %d tables reliées par convention de nommage. ", stats.Tables)
		b.WriteString("Les liens en pointillés et la liste des relations sont des <b>relations déduites</b> ")
		b.WriteString("(colonne <code>id_xxx</code> ou <code>fk_id_xxx</code> → <code>xxx.id_xxx</code>), ")
		b.WriteString("et non des contraintes réelles de la base.</div>")
	}
	if len(cat.Warnings) > 0 {
		b.WriteString("<details class='note'><summary>")
		fmt.Fprintf(&b, "%d avertissement(s) d'analyse", len(cat.Warnings))
		b.WriteString("</summary><ul>")
		for _, w := range cat.Warnings {
			fmt.Fprintf(&b, "<li>%s</li>", escape(w))
		}
		b.WriteString("</ul></details>")
	}

	b.WriteString("<section id='diagrammes'><h2>1. Diagramme du schéma</h2>")
	b.WriteString("<h3>Toutes les tables</h3>")
	fmt.Fprintf(&b, "<div class='diagram'>%s</div>", gridAll)
	if gridRelations != "" {
		b.WriteString("<h3>Tables reliées uniquement, avec les relations déduites</h3>")
		fmt.Fprintf(&b, "<div class='diagram'>%s</div>", gridRelations)
	}
	b.WriteString("<p class='note'>Dans les diagrammes, les noms de tables et de colonnes sont ")
	b.WriteString("tronqués à la largeur des cartes. Le détail complet est en section 5.</p>")
	b.WriteString("</section>")

	b.WriteString("<section id='relations'><h2>2. Relations</h2>")
	writeRelationsTable(&b, cat)
	b.WriteString("</section>")

	if len(cat.Views) > 0 {
		b.WriteString("<section id='vues'><h2>3. Vues</h2>")
		b.WriteString("<p class='note'>Une vue n'est pas analysée en profondeur : ses colonnes sont celles ")
		b.WriteString("que le serveur exposerait, déduites de la liste du SELECT quand elle est explicite. ")
		b.WriteString("Un <code>SELECT *</code> reste indéterminé, faute de connaître les tables sources.</p>")
		b.WriteString("<table><tr><th>Vue</th><th>Colonnes</th><th>Définition</th></tr>")
		for _, v := range cat.Views {
			columns := strings.Join(v.Columns, ", ")
			if columns == "" {
				columns = "<span class='dim'>indéterminées</span>"
			}
			definition := "—"
			if v.Definition != "" {
				definition = "<details><summary>voir</summary><pre class='sql'>" + escape(v.Definition) + "</pre></details>"
			}
			fmt.Fprintf(&b, "<tr><td class='mono'>%s</td><td class='mono'>%s</td><td>%s</td></tr>",
				escape(v.Name), columns, definition)
		}
		b.WriteString("</table></section>")
	}

	b.WriteString("<section id='index'><h2>4. Index des tables</h2><div class='toc'>")
	for _, t := range cat.Tables {
		fmt.Fprintf(&b, "<a href='#tbl-%s'>%s <span class='dim'>(%d)</span></a>",
			escape(t.Name), escape(t.Name), len(t.Columns))
	}
	b.WriteString("</div></section>")

	b.WriteString("<section id='tables'><h2>5. Détail des tables</h2>")
	for _, t := range cat.Tables {
		writeTable(&b, t)
	}
	b.WriteString("</section>")

	b.WriteString("<a class='top' href='#' title='Haut de page'>↑</a></main></body></html>")
	return b.String()
}

// RelationsSVG rend le diagramme des seules tables reliées.
func (r *Report) RelationsSVG() string {
	opt := r.Grid
	opt.Relations = r.Catalog.AllRelations()
	related := r.Catalog.RelatedTables()
	if len(related) == 0 {
		return ""
	}
	return GridSVG(related, opt)
}

// GridSVG rend le diagramme complet.
func (r *Report) GridSVG() string {
	opt := r.Grid
	opt.LinkTables = true
	return GridSVG(r.Catalog.Tables, opt)
}

func writeRelationsTable(b *strings.Builder, cat *catalog.Catalog) {
	declared := cat.AllRelations()
	implied := cat.ImpliedRelations
	declaredCount := len(declared) - len(implied)
	exists := map[string]bool{}
	for _, t := range cat.Tables {
		exists[strings.ToLower(t.Name)] = true
	}

	b.WriteString("<table><tr><th>Type</th><th>Table enfant</th><th>Colonne</th><th></th><th>Table parente</th><th>Colonne cible</th></tr>")
	if declaredCount == 0 {
		b.WriteString("<tr><td colspan='6'>Aucune contrainte déclarée.</td></tr>")
	}
	writeRelationRows(b, declared[:declaredCount], "déclarée", exists)
	if len(implied) > 0 {
		writeRelationRows(b, implied, "déduite", exists)
	}
	b.WriteString("</table>")
}

func writeRelationRows(b *strings.Builder, rels []catalog.Relation, kind string, exists map[string]bool) {
	for _, rel := range rels {
		known := "class='mono'"
		if !exists[strings.ToLower(rel.ParentTable)] {
			known = "class='mono missing'"
		}
		fmt.Fprintf(b, "<tr><td class='kind'>%s</td><td><a href='#tbl-%s'>%s</a></td><td class='mono'>%s</td><td>→</td><td %s><a href='#tbl-%s'>%s</a></td><td class='mono'>%s</td></tr>",
			escape(kind), escape(rel.ChildTable), escape(rel.ChildTable), escape(rel.ChildColumn),
			known, escape(rel.ParentTable), escape(rel.ParentTable), escape(rel.ParentColumn))
	}
}

func writeTable(b *strings.Builder, t *catalog.Table) {
	fmt.Fprintf(b, "<div class='card' id='tbl-%s'>", escape(t.Name))
	fmt.Fprintf(b, "<h3>%s <span class='dim'>%d colonnes", escape(t.Name), len(t.Columns))
	if t.Engine != "" {
		fmt.Fprintf(b, " &middot; %s", escape(t.Engine))
	}
	if t.Collation != "" {
		fmt.Fprintf(b, " &middot; %s", escape(t.Collation))
	}
	b.WriteString("</span></h3>")
	if t.Comment != "" {
		fmt.Fprintf(b, "<p class='comment'>%s</p>", escape(t.Comment))
	}
	b.WriteString("<table><tr><th>#</th><th>Colonne</th><th>Type</th><th>Charset / collation</th>")
	b.WriteString("<th>NULL</th><th>Défaut</th><th>Clé</th><th>Extra</th></tr>")
	for i, c := range t.Columns {
		charset := "—"
		if c.Charset != "" {
			charset = escape(c.Charset)
			if c.Collation != "" {
				charset += " / " + escape(c.Collation)
			}
		}
		extra := c.Extra
		if c.Generated != "" {
			if extra != "" {
				extra += " — "
			}
			extra += "AS (" + c.Generated + ")"
		}
		fmt.Fprintf(b, "<tr><td>%d</td><td class='mono'>%s</td><td class='mono'>%s</td><td class='mono dim'>%s</td><td>%s</td><td class='mono'>%s</td><td>%s</td><td class='mono dim'>%s</td></tr>",
			i+1, escape(c.Name), escape(c.Type), charset, nullable(c.Nullable), escape(c.DefaultString()),
			keyBadge(c.Key), escape(extra))
	}
	b.WriteString("</table>")

	if len(t.Indexes) > 0 {
		b.WriteString("<table class='sub'><tr><th>Index</th><th>Type</th><th>Colonnes</th></tr>")
		for _, ix := range t.Indexes {
			fmt.Fprintf(b, "<tr><td class='mono'>%s</td><td>%s</td><td class='mono'>%s</td></tr>",
				escape(ix.Name), indexKind(ix.Kind), escape(ix.String()))
		}
		b.WriteString("</table>")
	}
	if len(t.Checks) > 0 {
		b.WriteString("<table class='sub'><tr><th>Contrainte CHECK</th><th>Expression</th></tr>")
		for _, c := range t.Checks {
			fmt.Fprintf(b, "<tr><td class='mono'>%s</td><td class='mono'>%s</td></tr>", escape(c.Name), escape(c.Expr))
		}
		b.WriteString("</table>")
	}
	b.WriteString("</div>")
}

func nullable(n bool) string {
	if n {
		return "oui"
	}
	return "non"
}

func keyBadge(key string) string {
	switch key {
	case "PRI":
		return "<span class='badge'>PK</span>"
	case "UNI":
		return "<span class='badge'>UQ</span>"
	case "MUL":
		return "<span class='badge idx'>IX</span>"
	}
	return "—"
}

func indexKind(k catalog.Kind) string {
	switch k {
	case catalog.KindPrimary:
		return "PRIMARY KEY"
	case catalog.KindUnique:
		return "UNIQUE"
	case catalog.KindFulltext:
		return "FULLTEXT"
	case catalog.KindSpatial:
		return "SPATIAL"
	}
	return "KEY"
}

// baseName retire l'extension et le dossier d'un chemin de fichier.
func baseName(path string) string {
	name := path
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return name
}

const reportCSS = `
:root { color-scheme: light dark; --bg:#f7f7f9; --card:#fff; --line:#dcdfe4;
        --muted:#5b6472; --accent:#a8326b; --key:#fff3f7; }
@media (prefers-color-scheme: dark) {
  :root { --bg:#16181d; --card:#1e2126; --line:#333842; --muted:#9aa4b2;
          --accent:#e879a8; --key:#2a1c25; } }
* { box-sizing: border-box; }
body { margin:0; background:var(--bg); color:inherit;
       font:15px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif; }
header { padding:26px 24px 18px; background:var(--card); border-bottom:1px solid var(--line); }
h1 { margin:0 0 6px; font-size:25px; }
.stats { color:var(--muted); font-size:14px; }
.stats b { color:inherit; }
main { padding:24px; max-width:1280px; margin:0 auto; }
section { margin-bottom:44px; }
h2 { font-size:20px; border-bottom:2px solid var(--accent); padding-bottom:6px; margin:0 0 16px; }
h3 { font-size:15px; margin:22px 0 8px; color:var(--muted); }
.note { background:var(--card); border-left:4px solid var(--accent);
        padding:12px 16px; border-radius:6px; color:var(--muted); font-size:14px; margin-bottom:18px; }
.diagram { background:#fff; border:1px solid var(--line); border-radius:8px;
           overflow:auto; max-height:82vh; padding:8px; }
.diagram svg { display:block; width:auto; max-width:none; height:auto; }
details summary { cursor:pointer; font-weight:600; }
table { border-collapse:collapse; width:100%; background:var(--card);
        border:1px solid var(--line); border-radius:8px; overflow:hidden; font-size:13.5px; margin-bottom:16px; }
th, td { padding:6px 10px; text-align:left; border-bottom:1px solid var(--line); vertical-align:top; }
th { background:rgba(168,50,107,.10); font-weight:600; }
tr:last-child td { border-bottom:none; }
td.mono, .mono { font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace; }
td.kind { color:var(--muted); font-size:12px; }
.dim { color:var(--muted); font-weight:400; }
.missing { color:#b23; }
pre.sql { white-space:pre-wrap; font-size:12px; background:var(--bg); padding:8px; border-radius:6px; }
.badge { display:inline-block; padding:1px 7px; border-radius:10px; font-size:11px;
         font-weight:700; background:var(--key); color:var(--accent); }
.badge.idx { background:rgba(120,120,120,.15); color:var(--muted); }
.toc { columns:3; column-gap:24px; font-size:13.5px; }
.toc a { color:inherit; text-decoration:none; display:block; padding:1px 0; }
.toc a:hover { color:var(--accent); }
.card { background:var(--card); border:1px solid var(--line); border-radius:8px;
        padding:14px 16px; margin-bottom:18px; scroll-margin-top:10px; }
.card h3 { margin-top:0; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; color:inherit; }
.card table.sub { margin-top:10px; }
.comment { color:var(--muted); font-style:italic; margin:0 0 10px; }
.top { position:fixed; right:18px; bottom:18px; background:var(--accent); color:#fff;
       border-radius:50%; width:42px; height:42px; display:flex; align-items:center;
       justify-content:center; text-decoration:none; font-size:20px; box-shadow:0 2px 8px rgba(0,0,0,.3); }
`
