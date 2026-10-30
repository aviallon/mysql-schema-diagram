package infer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
	"github.com/aviallon/mysql-schema-diagram/internal/ddl"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
	"github.com/aviallon/mysql-schema-diagram/internal/infer"
)

// La fixture commitée est synthétique : aucun schéma de base réelle n'est
// publié. Les dumps réels, eux, ne sont jamais dans le dépôt (voir README).
const oracleDir = "../../testdata/oracle"

// TestDeclaredForeignKeyIsNotDuplicated vérifie qu'une contrainte réellement
// écrite dans le dump n'est pas présentée une seconde fois comme déduite.
func TestDeclaredForeignKeyIsNotDuplicated(t *testing.T) {
	cat := load(t, "edge_cases")
	found := false
	for _, rel := range cat.ImpliedRelations {
		if rel.ChildTable == "edge_child" && rel.ChildColumn == "parent_id" {
			t.Errorf("edge_child.parent_id est déclarée en FOREIGN KEY, elle ne doit pas être déduite : %+v", rel)
		}
		if rel.ChildTable == "edge_child" && rel.ChildColumn == "parent_code" {
			found = true
		}
	}
	if !found {
		t.Error("edge_child.parent_code devrait être déduite vers edge_parent.code")
	}
}

// TestBothNamingConventionsAreRecognised couvre les deux conventions que la
// documentation met en avant, sur la fixture commitée.
func TestBothNamingConventionsAreRecognised(t *testing.T) {
	cat := load(t, "edge_cases")
	want := map[string]string{
		"article.id_catalogue":    "catalogue",
		"article.fk_id_catalogue": "catalogue",
		"edge_child.parent_code":  "edge_parent",
	}
	got := map[string]string{}
	for _, rel := range cat.ImpliedRelations {
		got[rel.ChildTable+"."+rel.ChildColumn] = rel.ParentTable
	}
	for column, parent := range want {
		if got[column] != parent {
			t.Errorf("%s devrait viser %s, obtenu %q", column, parent, got[column])
		}
	}
	if len(cat.ImpliedRelations) < len(want) {
		t.Errorf("%d relations déduites, au moins %d attendues", len(cat.ImpliedRelations), len(want))
	}
}

// TestNoColumnHasTwoParents : une colonne enfant ne peut désigner qu'une seule
// clé. Deux liens partant de la même colonne sont le signe d'une ambiguïté mal
// traitée — c'est exactement ce qui se produisait avant le classement par
// confiance, quand deux tables voisines portaient la même clé et qu'un
// « fk_id_… » se raccrochait aux deux.
func TestNoColumnHasTwoParents(t *testing.T) {
	for _, name := range fixtures(t) {
		cat := load(t, name)
		seen := map[string]string{}
		for _, rel := range cat.ImpliedRelations {
			key := rel.ChildTable + "." + rel.ChildColumn
			if previous, ok := seen[key]; ok {
				t.Errorf("%s : la colonne %s vise à la fois %s et %s",
					name, key, previous, rel.ParentTable)
				continue
			}
			seen[key] = rel.ParentTable
		}
	}
}

// TestInferredRelationsPointToRealColumns : toute relation déduite doit désigner
// une table et une colonne qui existent. Une inférence qui pointe dans le vide
// serait pire qu'aucune inférence.
func TestInferredRelationsPointToRealColumns(t *testing.T) {
	for _, name := range fixtures(t) {
		cat := load(t, name)
		byName := map[string]*catalog.Table{}
		for _, tb := range cat.Tables {
			byName[tb.Name] = tb
		}
		for _, rel := range cat.ImpliedRelations {
			parent, ok := byName[rel.ParentTable]
			if !ok {
				t.Errorf("%s : la relation %s.%s pointe vers la table inconnue %q",
					name, rel.ChildTable, rel.ChildColumn, rel.ParentTable)
				continue
			}
			if parent.Column(rel.ParentColumn) == nil {
				t.Errorf("%s : la relation %s.%s pointe vers %s.%s, colonne inexistante",
					name, rel.ChildTable, rel.ChildColumn, rel.ParentTable, rel.ParentColumn)
			}
			child, ok := byName[rel.ChildTable]
			if !ok || child.Column(rel.ChildColumn) == nil {
				t.Errorf("%s : la relation part de %s.%s, inexistant",
					name, rel.ChildTable, rel.ChildColumn)
			}
		}
	}
}

// fixtures énumère les fixtures disponibles : celles du dépôt, plus celles d'un
// répertoire local de dumps réels s'il est renseigné.
func fixtures(t *testing.T) []string {
	t.Helper()
	dirs := []string{oracleDir}
	if local := localOracleDir(); local != "" {
		if info, err := os.Stat(local); err == nil && info.IsDir() {
			dirs = append(dirs, local)
		}
	}
	var out []string
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("lecture de %s : %v", dir, err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".sql") {
				out = append(out, filepath.Join(dir, strings.TrimSuffix(e.Name(), ".sql")))
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("aucune fixture trouvée")
	}
	return out
}

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

// load analyse une fixture désignée par son nom (dépôt) ou son chemin complet.
func load(t *testing.T, name string) *catalog.Catalog {
	t.Helper()
	path := filepath.Join(oracleDir, name+".sql")
	if strings.ContainsRune(name, os.PathSeparator) {
		path = name + ".sql"
	}
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

// TestPrefixMustNameTheTargetTable : un préfixe qui ressemble à un nom de table
// sans l'être ne doit pas créer de lien. « client_prospect_id » ne désigne pas
// « client » sous prétexte que le nom commence pareil — c'était un faux positif
// confiant de la règle par sous-chaîne.
func TestPrefixMustNameTheTargetTable(t *testing.T) {
	cat := buildFromSQL(t, `CREATE TABLE `+"`client`"+` (`+"`id`"+` int NOT NULL, PRIMARY KEY (`+"`id`"+`));
CREATE TABLE `+"`prospect`"+` (`+"`id_prospect`"+` int NOT NULL, PRIMARY KEY (`+"`id_prospect`"+`));
CREATE TABLE `+"`commande`"+` (`+"`id_commande`"+` int NOT NULL, `+"`client_prospect_id`"+` int NOT NULL, PRIMARY KEY (`+"`id_commande`"+`), KEY `+"`k_cp`"+` (`+"`client_prospect_id`"+`));`)
	for _, rel := range cat.ImpliedRelations {
		if rel.ChildColumn != "client_prospect_id" {
			continue
		}
		t.Errorf("faux positif : %s.%s viserait %s.%s",
			rel.ChildTable, rel.ChildColumn, rel.ParentTable, rel.ParentColumn)
	}
}

// TestAmbiguityIsRefusedNotGuessed doit produire une ambiguïté RÉELLE, sinon il
// ne vérifie rien : la version précédente bouclait sur zéro avertissement et
// passait donc sans jamais exercer le refus.
func TestAmbiguityIsRefusedNotGuessed(t *testing.T) {
	cat := buildFromSQL(t, `CREATE TABLE `+"`alpha`"+` (`+"`id_commun`"+` int NOT NULL, PRIMARY KEY (`+"`id_commun`"+`));
CREATE TABLE `+"`beta`"+` (`+"`id_commun`"+` int NOT NULL, PRIMARY KEY (`+"`id_commun`"+`));
CREATE TABLE `+"`gamma`"+` (`+"`id_gamma`"+` int NOT NULL, `+"`id_commun`"+` int NOT NULL, PRIMARY KEY (`+"`id_gamma`"+`));`)

	relations, warnings := infer.InferWithWarnings(cat)
	for _, rel := range relations {
		if rel.ChildTable == "gamma" && rel.ChildColumn == "id_commun" {
			t.Errorf("une relation ambiguë ne doit pas être devinée : %s.%s → %s",
				rel.ChildTable, rel.ChildColumn, rel.ParentTable)
		}
	}
	if len(warnings) == 0 {
		t.Fatal("aucune ambiguïté signalée alors que deux tables portent la clé id_commun : le refus n'est pas exercé")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "ambiguë") {
			found = true
		}
	}
	if !found {
		t.Errorf("l'avertissement d'ambiguïté est absent : %v", warnings)
	}
}

func buildFromSQL(t *testing.T, sql string) *catalog.Catalog {
	t.Helper()
	d, err := dialect.Get("mysql-5.7")
	if err != nil {
		t.Fatalf("dialecte : %v", err)
	}
	parsed, err := ddl.Parse(sql)
	if err != nil {
		t.Fatalf("analyse SQL : %v", err)
	}
	cat, _ := catalog.Build(parsed.Statements, d, "inline.sql")
	cat.ImpliedRelations, _ = infer.InferWithWarnings(cat)
	cat.Sort()
	return cat
}
