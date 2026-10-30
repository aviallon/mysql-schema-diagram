package catalog_test

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
	"github.com/aviallon/mysql-schema-diagram/internal/ddl"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
)

// Les fixtures sont chargées dans un vrai serveur MariaDB par
// testdata/regenerate-oracle.sh, qui écrit les TSV de référence. Ce test
// compare le catalogue hors-ligne à cette référence : c'est lui qui décide si la
// lecture du DDL est fidèle.
//
// oracleDir est le seul répertoire commité. Les dumps de bases réelles, eux,
// vivent HORS du dépôt (MYSQL_SCHEMA_DIAGRAM_ORACLE_DIR, par défaut
// ~/.local/share/mysql-schema-diagram/oracle) : un schéma de production n'a rien à faire
// dans un dépôt, même privé, et encore moins dans son historique.
const oracleDir = "../../testdata/oracle"

// knownDifferences recense les écarts assumés, avec leur raison. Un écart qui
// n'est pas listé ici fait échouer le test : c'est le garde contre la
// dérive silencieuse.
var knownDifferences = map[string]string{}

type fixtureRef struct {
	name string
	dir  string
}

// committed désigne une fixture du dépôt (synthétique).
func committed(name string) fixtureRef { return fixtureRef{name: name, dir: oracleDir} }

func TestOracleColumnFidelity(t *testing.T) {
	for _, ref := range fixtures(t) {
		ref := ref
		t.Run(ref.name, func(t *testing.T) {
			cat := buildFixture(t, ref)

			views := map[string]bool{}
			for _, v := range cat.Views {
				views[strings.ToLower(v.Name)] = true
			}
			tables := map[string]*catalog.Table{}
			for _, tb := range cat.Tables {
				tables[strings.ToLower(tb.Name)] = tb
			}

			rows := readTSV(t, filepath.Join(ref.dir, ref.name+".columns.tsv"))
			seen := 0
			for _, row := range rows {
				name := strings.ToLower(row[0])
				if views[name] {
					continue
				}
				table, ok := tables[name]
				if !ok {
					t.Errorf("la table %q existe dans l'oracle mais pas dans le catalogue", row[0])
					continue
				}
				col := table.Column(row[2])
				if col == nil {
					t.Errorf("%s.%s : colonne absente du catalogue", row[0], row[2])
					continue
				}
				seen++
				label := row[0] + "." + row[2]
				checkField(t, label, "type", row[3], col.Type)
				checkField(t, label, "nullable", row[4], yesNo(col.Nullable))
				checkField(t, label, "key", row[5], col.Key)
				checkField(t, label, "default", row[6], col.DefaultString())
				checkField(t, label, "extra", row[7], col.Extra)
				checkField(t, label, "charset", row[8], sqlNull(col.Charset))
				checkField(t, label, "collation", row[9], sqlNull(col.Collation))
				checkField(t, label, "generated", row[10], sqlNull(col.Generated))
				checkField(t, label, "comment", row[11], col.Comment)
			}

			total := 0
			for _, tb := range cat.Tables {
				total += len(tb.Columns)
			}
			if seen != total {
				t.Errorf("%d colonnes comparées sur %d produites : le catalogue en contient qui ne sont pas dans l'oracle", seen, total)
			}
		})
	}
}

func TestOracleIndexFidelity(t *testing.T) {
	for _, ref := range fixtures(t) {
		ref := ref
		t.Run(ref.name, func(t *testing.T) {
			cat := buildFixture(t, ref)
			tables := map[string]*catalog.Table{}
			for _, tb := range cat.Tables {
				tables[strings.ToLower(tb.Name)] = tb
			}

			for _, row := range readTSV(t, filepath.Join(ref.dir, ref.name+".indexes.tsv")) {
				table, ok := tables[strings.ToLower(row[0])]
				if !ok {
					continue // vue
				}
				label := row[0] + "." + row[1] + "[" + row[3] + "]"
				ix := findIndex(table, row[1])
				if ix == nil {
					t.Errorf("%s : index absent du catalogue", label)
					continue
				}
				if col := indexColumn(ix, row[1], row[4]); col == nil {
					t.Errorf("%s : colonne %q absente de l'index", label, row[4])
					continue
				}
				// L'ordre compte : un index (a,b) n'est pas un index (b,a).
				position, convErr := strconv.Atoi(row[3])
				if convErr == nil && position >= 1 && position <= len(ix.Columns) {
					if !strings.EqualFold(ix.Columns[position-1].Name, row[4]) {
						t.Errorf("%s : position %d vaut %q dans le catalogue, %q dans l'oracle",
							label, position, ix.Columns[position-1].Name, row[4])
					}
				}
				checkField(t, label, "sub_part", row[5], prefixString(indexPrefix(ix, row[4])))
				checkField(t, label, "index_type", row[6], oracleIndexType(ix.Kind))
			}

			produced := 0
			for _, tb := range cat.Tables {
				produced += len(tb.Indexes)
			}
			if got := countOracleIndexes(t, ref); got != produced {
				t.Errorf("%d index dans l'oracle, %d produits", got, produced)
			}
		})
	}
}

func TestOracleForeignKeyFidelity(t *testing.T) {
	for _, ref := range fixtures(t) {
		ref := ref
		t.Run(ref.name, func(t *testing.T) {
			cat := buildFixture(t, ref)
			expected := readTSV(t, filepath.Join(ref.dir, ref.name+".foreignkeys.tsv"))
			seen := map[string]bool{}
			for _, tb := range cat.Tables {
				for _, fk := range tb.ForeignKeys {
					for i, col := range fk.Columns {
						ref := ""
						if i < len(fk.RefColumns) {
							ref = fk.RefColumns[i]
						}
						seen[strings.Join([]string{tb.Name, fk.Name, col, fk.RefTable, ref}, "|")] = true
					}
				}
			}
			for _, row := range expected {
				key := strings.Join(row, "|")
				if !seen[key] {
					t.Errorf("clé étrangère %s manquante dans le catalogue", key)
				}
				delete(seen, key)
			}
			for key := range seen {
				t.Errorf("clé étrangère %s présente dans le catalogue mais pas dans l'oracle", key)
			}
		})
	}
}

// buildFixture analyse une fixture avec le dialecte MariaDB, celui du serveur qui
// a produit l'oracle.
func buildFixture(t *testing.T, ref fixtureRef) *catalog.Catalog {
	t.Helper()
	d, err := dialect.Get("mariadb-10.6")
	if err != nil {
		t.Fatalf("dialecte : %v", err)
	}
	parsed, err := ddl.ReadFile(filepath.Join(ref.dir, ref.name+".sql"))
	if err != nil {
		t.Fatalf("lecture de %s : %v", ref.name, err)
	}
	cat, warnings := catalog.Build(parsed.Statements, d, ref.name+".sql")
	if len(warnings) > 0 {
		t.Logf("avertissements d'analyse : %v", warnings)
	}
	return cat
}

// localOracleDir renvoie le répertoire des dumps réels, hors du dépôt.
func localOracleDir() string {
	if dir := os.Getenv("MYSQL_SCHEMA_DIAGRAM_ORACLE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "mysql-schema-diagram", "oracle")
}

// fixtures énumère les fixtures réellement comparables : un .sql accompagné de
// son oracle .columns.tsv. Une fixture sans oracle est signalée, jamais ignorée
// en silence — sans quoi un oracle effacé rendrait le test vert pour rien.
func fixtures(t *testing.T) []fixtureRef {
	t.Helper()
	dirs := []string{oracleDir}
	if local := localOracleDir(); local != "" {
		if info, err := os.Stat(local); err == nil && info.IsDir() {
			dirs = append(dirs, local)
		}
	}

	var out []fixtureRef
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("lecture de %s : %v", dir, err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".sql")
			if _, err := os.Stat(filepath.Join(dir, name+".columns.tsv")); err != nil {
				t.Fatalf("%s : fixture sans oracle %s.columns.tsv — régénérez-le (make oracle)", dir, name)
			}
			out = append(out, fixtureRef{name: name, dir: dir})
		}
	}
	if len(out) == 0 {
		t.Fatal("aucune fixture trouvée : le test ne prouverait rien")
	}
	return out
}

func readTSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("ouverture de %s : %v", path, err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("lecture de %s : %v", path, err)
	}
	if len(rows) == 0 {
		return nil
	}
	return rows[1:] // en-tête
}

func checkField(t *testing.T, label, field, want, got string) {
	t.Helper()
	if want == got {
		return
	}
	key := label + "." + field
	if reason, ok := knownDifferences[key]; ok {
		t.Logf("écart connu pour %s : %s (oracle %q, catalogue %q)", key, reason, want, got)
		return
	}
	t.Errorf("%s %s : oracle %q, catalogue %q", label, field, want, got)
}

func yesNo(b bool) string {
	if b {
		return "YES"
	}
	return "NO"
}

// sqlNull rend la valeur comme le client mariadb le fait pour un NULL SQL.
func sqlNull(s string) string {
	if s == "" {
		return "NULL"
	}
	return s
}

func findIndex(t *catalog.Table, name string) *catalog.Index {
	for _, ix := range t.Indexes {
		if strings.EqualFold(ix.Name, name) {
			return ix
		}
	}
	return nil
}

func indexColumn(ix *catalog.Index, _ string, column string) *catalog.IndexColumn {
	for i := range ix.Columns {
		if strings.EqualFold(ix.Columns[i].Name, column) {
			return &ix.Columns[i]
		}
	}
	return nil
}

func indexPrefix(ix *catalog.Index, column string) int {
	if c := indexColumn(ix, ix.Name, column); c != nil {
		return c.Prefix
	}
	return 0
}

func prefixString(p int) string {
	if p <= 0 {
		return ""
	}
	return strconv.Itoa(p)
}

func oracleIndexType(k catalog.Kind) string {
	switch k {
	case catalog.KindFulltext:
		return "FULLTEXT"
	case catalog.KindSpatial:
		return "RTREE"
	}
	return "BTREE"
}

func countOracleIndexes(t *testing.T, ref fixtureRef) int {
	t.Helper()
	rows := readTSV(t, filepath.Join(ref.dir, ref.name+".indexes.tsv"))
	names := map[string]bool{}
	for _, row := range rows {
		names[row[0]+"."+row[1]] = true
	}
	return len(names)
}
