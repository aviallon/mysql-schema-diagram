package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
)

// finalizeTable applique ce que le serveur ferait à la création : héritage des
// jeux de caractères, normalisation liée à la version, index implicites pour les
// contraintes déclarées en ligne, puis marquage de la colonne « Key ».
func finalizeTable(t *Table, d dialect.Dialect, warnings *[]string) {
	for i, c := range t.Columns {
		c.Ordinal = i + 1
		inheritCharset(t, c, d)
		c.Type = normalizeType(c.Type, d)
		if d.TextBlobDefaultForbidden && c.Default != nil && defaultForbiddenType(c.Type) {
			*warnings = append(*warnings, fmt.Sprintf(
				"%s.%s : DEFAULT sur une colonne %s n'est pas accepté avant MySQL 8.0.13",
				t.Name, c.Name, baseType(c.Type)))
		}
	}
	synthesizeInlineIndexes(t)
	synthesizeForeignKeyIndexes(t)
	markColumnKeys(t)
}

// inheritCharset applique le jeu de caractères et la collation que le serveur
// retient pour une colonne textuelle. Trois cas, dans cet ordre :
//
//  1. COLLATE explicite sur la colonne : rien à faire.
//  2. CHARACTER SET explicite sur la colonne sans COLLATE : la collation est
//     celle par défaut de ce jeu de caractères, PAS celle de la table.
//  3. rien d'explicite : la collation de la table, ou celle par défaut du jeu
//     de caractères de la table.
//
// Vérifié par l'oracle : edge_types.payload est « longtext CHARACTER SET utf8 »
// dans une table utf8mb4, et le serveur rapporte utf8mb3_general_ci — la
// collation du jeu déclaré, pas celle de la table.
func inheritCharset(t *Table, c *Column, d dialect.Dialect) {
	if !hasCharsetType(c.Type) {
		return
	}
	declaredCharset := c.Charset != ""
	if !declaredCharset {
		c.Charset = t.Charset
	}
	c.Charset = normalizeCharset(c.Charset, d)
	if c.Collation != "" {
		// Une collation explicite sans jeu de caractères impose le jeu : le
		// couple « charset de la table + collation d'un autre jeu » n'existe pas.
		c.Collation = normalizeCharset(c.Collation, d)
		if !declaredCharset {
			if derived := charsetOfCollation(c.Collation); derived != "" {
				c.Charset = normalizeCharset(derived, d)
			}
		}
		return
	}
	switch {
	case declaredCharset:
		c.Collation = d.DefaultCollation(c.Charset)
	case t.Collation != "":
		c.Collation = normalizeCharset(t.Collation, d)
	default:
		c.Collation = d.DefaultCollation(c.Charset)
	}
}

// charsetOfCollation déduit le jeu de caractères d'un nom de collation, comme le
// fait le serveur quand une colonne déclare COLLATE sans CHARACTER SET.
func charsetOfCollation(collation string) string {
	name := strings.ToLower(strings.TrimSpace(collation))
	switch {
	case name == "", name == "binary":
		return name
	case strings.HasPrefix(name, "utf8mb4_"):
		return "utf8mb4"
	case strings.HasPrefix(name, "utf8mb3_"):
		return "utf8mb3"
	case strings.HasPrefix(name, "utf8_"):
		return "utf8"
	}
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return ""
}

// hasCharsetType indique si la colonne porte un jeu de caractères. Les types
// binaires (BLOB, BINARY, VARBINARY) n'en ont pas : information_schema y laisse
// CHARACTER_SET_NAME et COLLATION_NAME à NULL.
func hasCharsetType(columnType string) bool {
	switch baseType(columnType) {
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext", "enum", "set":
		return true
	}
	return false
}

// defaultForbiddenType reprend la liste exacte des types qui refusaient une
// valeur par défaut avant MySQL 8.0.13. VARCHAR et CHAR n'en font pas partie.
func defaultForbiddenType(columnType string) bool {
	switch baseType(columnType) {
	case "tinytext", "text", "mediumtext", "longtext",
		"tinyblob", "blob", "mediumblob", "longblob",
		"geometry", "point", "linestring", "polygon", "json":
		return true
	}
	return false
}

var integerWidth = regexp.MustCompile(`(?i)^(tinyint|smallint|mediumint|int|integer|bigint)\((\d+)\)`)

// normalizeType applique les différences d'affichage liées à la version visée.
func normalizeType(columnType string, d dialect.Dialect) string {
	if !d.KeepIntegerDisplayWidth {
		if m := integerWidth.FindStringSubmatch(columnType); m != nil {
			// MySQL conserve la largeur de TINYINT(1) : elle vaut marqueur booléen.
			if !(strings.EqualFold(m[1], "tinyint") && m[2] == "1") {
				columnType = columnType[:len(m[1])] + columnType[len(m[0]):]
			}
		}
		// MySQL 8.0.19+ ignore aussi la largeur de YEAR ; MariaDB la conserve.
		if strings.HasPrefix(strings.ToLower(columnType), "year(") {
			if i := strings.IndexByte(columnType, ')'); i >= 0 {
				columnType = "year" + columnType[i+1:]
			}
		}
	}
	return columnType
}

// normalizeCharset renomme l'alias utf8 en utf8mb3 sur les serveurs récents.
func normalizeCharset(name string, d dialect.Dialect) string {
	if !d.UTF8IsUTF8MB3 {
		return name
	}
	lower := strings.ToLower(name)
	if lower == "utf8" {
		return "utf8mb3"
	}
	if strings.HasPrefix(lower, "utf8_") {
		return "utf8mb3_" + name[len("utf8_"):]
	}
	return name
}

// synthesizeInlineIndexes recrée les index que MySQL crée pour une contrainte
// déclarée au niveau de la colonne (PRIMARY KEY ou UNIQUE en ligne).
func synthesizeInlineIndexes(t *Table) {
	var priCols []string
	for _, c := range t.Columns {
		switch c.Key {
		case "PRI":
			priCols = append(priCols, c.Name)
		case "UNI":
			exists := false
			for _, ix := range t.Indexes {
				if ix.Kind == KindUnique && len(ix.Columns) == 1 && strings.EqualFold(ix.Columns[0].Name, c.Name) {
					exists = true
					break
				}
			}
			if !exists {
				t.Indexes = append(t.Indexes, &Index{
					Name:    c.Name,
					Kind:    KindUnique,
					Columns: []IndexColumn{{Name: c.Name}},
				})
			}
		}
	}
	if len(priCols) > 0 && t.PrimaryKey() == nil {
		cols := make([]IndexColumn, 0, len(priCols))
		for _, name := range priCols {
			cols = append(cols, IndexColumn{Name: name})
		}
		t.Indexes = append([]*Index{{Name: "PRIMARY", Kind: KindPrimary, Columns: cols}}, t.Indexes...)
	}
}

// synthesizeForeignKeyIndexes nomme les clés étrangères anonymes comme le serveur
// le fait (enfant_ibfk_n) et ajoute l'index que MySQL crée automatiquement
// lorsqu'il n'existe pas déjà un index commençant par ses colonnes.
func synthesizeForeignKeyIndexes(t *Table) {
	fkNumber := 0
	for i, fk := range t.ForeignKeys {
		fkNumber++
		if fk.Name == "" {
			t.ForeignKeys[i].Name = fmt.Sprintf("%s_ibfk_%d", t.Name, fkNumber)
		}
		if len(fk.Columns) == 0 {
			continue
		}
		if hasIndexPrefix(t, fk.Columns) {
			continue
		}
		name := t.ForeignKeys[i].Name
		cols := make([]IndexColumn, 0, len(fk.Columns))
		for _, c := range fk.Columns {
			cols = append(cols, IndexColumn{Name: c})
		}
		t.Indexes = append(t.Indexes, &Index{Name: name, Kind: KindKey, Columns: cols})
	}
}

func hasIndexPrefix(t *Table, columns []string) bool {
	for _, ix := range t.Indexes {
		if len(ix.Columns) < len(columns) {
			continue
		}
		ok := true
		for i, want := range columns {
			if !strings.EqualFold(ix.Columns[i].Name, want) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// markColumnKeys calcule information_schema.COLUMNS.COLUMN_KEY.
//
// Deux règles, toutes deux mesurées sur l'oracle : seule la **première** colonne
// d'un index reçoit une marque (une colonne présente uniquement en position ≥ 2
// laisse COLUMN_KEY vide), et UNI n'est posé que si l'index unique ne porte que
// sur cette colonne-là.
//
// Quand une table n'a pas de clé primaire, le serveur promeut la première clé
// unique ne portant que sur des colonnes NOT NULL : c'est elle qu'il désigne
// comme clé de la table, donc PRI.
func markColumnKeys(t *Table) {
	for _, c := range t.Columns {
		c.Key = ""
	}
	pri := map[string]bool{}
	for _, name := range t.PrimaryKeyColumns() {
		pri[strings.ToLower(name)] = true
	}
	if len(pri) == 0 {
		if promoted := firstNotNullUniqueIndex(t); promoted != nil {
			pri[strings.ToLower(promoted.Columns[0].Name)] = true
		}
	}
	for _, c := range t.Columns {
		if pri[strings.ToLower(c.Name)] {
			c.Key = "PRI"
		}
	}
	for _, ix := range t.Indexes {
		if ix.Kind == KindPrimary || len(ix.Columns) == 0 {
			continue
		}
		col := t.Column(ix.Columns[0].Name)
		if col == nil || col.Key == "PRI" {
			continue
		}
		if ix.Kind == KindUnique && len(ix.Columns) == 1 {
			col.Key = "UNI"
			continue
		}
		if col.Key == "" {
			col.Key = "MUL"
		}
	}
}

// firstNotNullUniqueIndex renvoie la première clé unique à une seule colonne
// NOT NULL — celle que le serveur promeut en clé de table à défaut de primaire.
func firstNotNullUniqueIndex(t *Table) *Index {
	for _, ix := range t.Indexes {
		if ix.Kind != KindUnique || len(ix.Columns) != 1 {
			continue
		}
		if col := t.Column(ix.Columns[0].Name); col != nil && !col.Nullable {
			return ix
		}
	}
	return nil
}
