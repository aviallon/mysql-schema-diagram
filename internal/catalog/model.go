// Package catalog définit le modèle normalisé d'un schéma physique, tel qu'il
// est déduit d'un dump SQL sans jamais se connecter à un serveur.
package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// Catalog est le résultat complet de l'analyse d'un dump.
type Catalog struct {
	// Source est le chemin du fichier analysé.
	Source string `json:"source"`
	// Dialect est l'identifiant du dialecte appliqué ("mysql-5.7", ...).
	Dialect string `json:"dialect"`
	// Tables est triée par nom.
	Tables []*Table `json:"tables"`
	// Views est triée par nom.
	Views []*View `json:"views"`
	// ImpliedRelations liste les relations déduites d'une convention de nommage,
	// donc absentes du dump. Elle est remplie par le paquet infer.
	ImpliedRelations []Relation `json:"implied_relations"`
	// Warnings liste ce qui n'a pas pu être représenté fidèlement.
	Warnings []string `json:"warnings"`
}

// Relation est un lien enfant -> parent, déclaré ou déduit.
type Relation struct {
	ChildTable   string `json:"child_table"`
	ChildColumn  string `json:"child_column"`
	ParentTable  string `json:"parent_table"`
	ParentColumn string `json:"parent_column"`
}

// Table est une table physique.
type Table struct {
	Name        string        `json:"name"`
	Engine      string        `json:"engine"`
	Charset     string        `json:"charset"`
	Collation   string        `json:"collation"`
	Comment     string        `json:"comment"`
	Columns     []*Column     `json:"columns"`
	Indexes     []*Index      `json:"indexes"`
	ForeignKeys []*ForeignKey `json:"foreign_keys"`
	Checks      []*Check      `json:"checks"`
}

// Column renvoie la colonne de ce nom, ou nil.
func (t *Table) Column(name string) *Column {
	for _, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return c
		}
	}
	return nil
}

// PrimaryKey renvoie l'index primaire, ou nil.
func (t *Table) PrimaryKey() *Index {
	for _, ix := range t.Indexes {
		if ix.Kind == KindPrimary {
			return ix
		}
	}
	return nil
}

// PrimaryKeyColumns renvoie la liste ordonnée des colonnes de la clé primaire.
func (t *Table) PrimaryKeyColumns() []string {
	pk := t.PrimaryKey()
	if pk == nil {
		return nil
	}
	names := make([]string, 0, len(pk.Columns))
	for _, c := range pk.Columns {
		names = append(names, c.Name)
	}
	return names
}

// Column est une colonne physique, avec les attributs que rendrait
// information_schema.COLUMNS.
type Column struct {
	Ordinal int    `json:"ordinal"`
	Name    string `json:"name"`
	// Type est la colonne COLUMN_TYPE : "int(11)", "varchar(50)", "enum('a','b')".
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	// Default est nil quand information_schema renverrait NULL (pas de valeur
	// par défaut). Une chaîne est renvoyée telle quelle, guillemets compris,
	// exactement comme le ferait le serveur.
	Default   *string `json:"default"`
	Extra     string  `json:"extra"`
	Key       string  `json:"key"`
	Charset   string  `json:"charset"`
	Collation string  `json:"collation"`
	Comment   string  `json:"comment"`
	// Generated contient l'expression d'une colonne générée, préfixée de
	// VIRTUAL ou STORED, comme le fait information_schema.GENERATION_EXPRESSION.
	Generated string `json:"generated"`
}

// DefaultString rend la valeur par défaut telle que la donnerait
// information_schema.COLUMNS.COLUMN_DEFAULT, c'est-à-dire « NULL » quand il n'y
// en a pas.
func (c *Column) DefaultString() string {
	if c.Default == nil {
		return "NULL"
	}
	return *c.Default
}

// Kind est la nature d'un index.
type Kind string

// Natures d'index reconnues, alignées sur les valeurs de SHOW INDEX.
const (
	KindPrimary  Kind = "PRIMARY"
	KindUnique   Kind = "UNIQUE"
	KindKey      Kind = "KEY"
	KindFulltext Kind = "FULLTEXT"
	KindSpatial  Kind = "SPATIAL"
)

// Index est un index (clé primaire comprise).
type Index struct {
	Name    string        `json:"name"`
	Kind    Kind          `json:"kind"`
	Columns []IndexColumn `json:"columns"`
	Comment string        `json:"comment"`
}

// IndexColumn est une colonne d'index, avec sa longueur de préfixe éventuelle.
type IndexColumn struct {
	Name string `json:"name"`
	// Prefix vaut 0 quand l'index porte sur la colonne entière.
	Prefix int  `json:"prefix,omitempty"`
	Desc   bool `json:"desc,omitempty"`
}

// String rend l'index sous une forme proche de SHOW CREATE TABLE.
func (i *Index) String() string {
	parts := make([]string, 0, len(i.Columns))
	for _, c := range i.Columns {
		if c.Prefix > 0 {
			parts = append(parts, fmt.Sprintf("%s(%d)", c.Name, c.Prefix))
		} else {
			parts = append(parts, c.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// ForeignKey est une contrainte de clé étrangère, déclarée ou déduite.
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
	OnDelete   string   `json:"on_delete,omitempty"`
	OnUpdate   string   `json:"on_update,omitempty"`
	// Implied vaut true quand la contrainte n'est pas écrite dans le dump mais
	// a été déduite d'une convention de nommage.
	Implied bool `json:"implied,omitempty"`
}

// Check est une contrainte CHECK.
type Check struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

// View est une vue.
type View struct {
	Name       string   `json:"name"`
	Definition string   `json:"definition"`
	Columns    []string `json:"columns"`
}

// Stats regroupe les compteurs affichés dans l'en-tête du rapport.
type Stats struct {
	Tables          int
	Columns         int
	Views           int
	Indexes         int
	DeclaredFK      int
	ImpliedFK       int
	TablesWithoutPK int
}

// Stats calcule les compteurs du catalogue.
func (c *Catalog) Stats() Stats {
	var s Stats
	s.Tables = len(c.Tables)
	s.Views = len(c.Views)
	for _, t := range c.Tables {
		s.Columns += len(t.Columns)
		s.Indexes += len(t.Indexes)
		if t.PrimaryKey() == nil {
			s.TablesWithoutPK++
		}
		for _, fk := range t.ForeignKeys {
			if !fk.Implied {
				s.DeclaredFK++
			}
		}
	}
	s.ImpliedFK = len(c.ImpliedRelations)
	return s
}

// AllRelations renvoie les contraintes réellement déclarées, suivies des
// relations déduites. Chaque élément porte le nom de la table enfant, ce qui
// permet de les afficher ensemble.
func (c *Catalog) AllRelations() []Relation {
	var out []Relation
	for _, t := range c.Tables {
		for _, fk := range t.ForeignKeys {
			if fk.Implied {
				continue
			}
			for i, col := range fk.Columns {
				ref := ""
				if i < len(fk.RefColumns) {
					ref = fk.RefColumns[i]
				}
				out = append(out, Relation{
					ChildTable:   t.Name,
					ChildColumn:  col,
					ParentTable:  fk.RefTable,
					ParentColumn: ref,
				})
			}
		}
	}
	out = append(out, c.ImpliedRelations...)
	return out
}

// RelatedTables renvoie le sous-ensemble des tables citées par au moins une
// relation, dans l'ordre du catalogue.
func (c *Catalog) RelatedTables() []*Table {
	involved := map[string]bool{}
	for _, rel := range c.AllRelations() {
		involved[strings.ToLower(rel.ChildTable)] = true
		involved[strings.ToLower(rel.ParentTable)] = true
	}
	var out []*Table
	for _, t := range c.Tables {
		if involved[strings.ToLower(t.Name)] {
			out = append(out, t)
		}
	}
	return out
}

// Sort trie les tables et les vues par nom, et les colonnes par ordinal.
func (c *Catalog) Sort() {
	sort.Slice(c.Tables, func(i, j int) bool { return c.Tables[i].Name < c.Tables[j].Name })
	sort.Slice(c.Views, func(i, j int) bool { return c.Views[i].Name < c.Views[j].Name })
	for _, t := range c.Tables {
		sort.SliceStable(t.Columns, func(i, j int) bool { return t.Columns[i].Ordinal < t.Columns[j].Ordinal })
	}
	c.ImpliedRelations = uniqueRelations(c.ImpliedRelations)
}

func uniqueRelations(in []Relation) []Relation {
	seen := make(map[Relation]bool, len(in))
	out := in[:0]
	for _, r := range in {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}
