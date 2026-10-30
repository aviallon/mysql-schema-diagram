package catalog_test

import (
	"strings"
	"testing"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
	"github.com/aviallon/mysql-schema-diagram/internal/ddl"
	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
)

// TestDialectHasObservableEffect est le garde contre une option de version
// décorative : le même fichier lu avec deux dialectes doit produire un catalogue
// différent, sur des points précis et vérifiables.
//
// Si un jour le dialecte n'était plus propagé jusqu'à la normalisation, ce test
// échouerait — c'est sa raison d'être.
func TestDialectHasObservableEffect(t *testing.T) {
	sql := "CREATE TABLE `t` (`id` int(11) NOT NULL AUTO_INCREMENT," +
		" `n` varchar(10) CHARACTER SET utf8 NOT NULL," +
		" `ts` timestamp NOT NULL, PRIMARY KEY (`id`)) ENGINE=InnoDB DEFAULT CHARSET=utf8;"

	mysql57 := buildFromSQL(t, sql, "mysql-5.7")
	mysql80 := buildFromSQL(t, sql, "mysql-8.0")

	id57 := columnOf(t, mysql57, "t", "id")
	id80 := columnOf(t, mysql80, "t", "id")
	if id57.Type != "int(11)" {
		t.Errorf("MySQL 5.7 doit conserver la largeur d'affichage, obtenu %q", id57.Type)
	}
	if id80.Type != "int" {
		t.Errorf("MySQL 8.0.19+ ignore la largeur d'affichage, obtenu %q", id80.Type)
	}

	n57 := columnOf(t, mysql57, "t", "n")
	n80 := columnOf(t, mysql80, "t", "n")
	if n57.Charset != "utf8" {
		t.Errorf("MySQL 5.7 nomme le jeu utf8, obtenu %q", n57.Charset)
	}
	if n80.Charset != "utf8mb3" {
		t.Errorf("MySQL 8.0.29+ renomme utf8 en utf8mb3, obtenu %q", n80.Charset)
	}

	ts57 := columnOf(t, mysql57, "t", "ts")
	ts80 := columnOf(t, mysql80, "t", "ts")
	if ts57.DefaultString() != "current_timestamp()" {
		t.Errorf("premier TIMESTAMP en 5.7 : défaut implicite attendu, obtenu %q", ts57.DefaultString())
	}
	if ts80.DefaultString() != "NULL" {
		t.Errorf("MySQL 8.0 n'ajoute pas de défaut implicite, obtenu %q", ts80.DefaultString())
	}
}

// TestImplicitTimestampRule couvre les deux branches de la règle, telles que le
// serveur les applique réellement (cf. edge_timestamps dans l'oracle).
func TestImplicitTimestampRule(t *testing.T) {
	cat := buildFixture(t, committed("edge_cases"))

	first := columnOf(t, cat, "edge_timestamps", "ts_first")
	if first.DefaultString() != "current_timestamp()" {
		t.Errorf("ts_first : défaut %q, attendu current_timestamp()", first.DefaultString())
	}
	if !strings.Contains(first.Extra, "on update current_timestamp()") {
		t.Errorf("ts_first : extra %q, attendu « on update current_timestamp() »", first.Extra)
	}

	later := columnOf(t, cat, "edge_timestamps", "ts_later")
	if later.DefaultString() != "'0000-00-00 00:00:00'" {
		t.Errorf("ts_later : défaut %q, attendu le TIMESTAMP zéro", later.DefaultString())
	}

	nullable := columnOf(t, cat, "edge_timestamps", "ts_nullable")
	if !nullable.Nullable {
		t.Error("ts_nullable : une colonne déclarée NULL doit rester nullable")
	}
	if nullable.DefaultString() != "NULL" {
		t.Errorf("ts_nullable : la règle ne doit pas s'appliquer, défaut %q", nullable.DefaultString())
	}
}

// TestUniqueKeyOnlyForSingleColumn grave le comportement mesuré : la première
// colonne d'un index unique composite est marquée MUL, pas UNI.
func TestUniqueKeyOnlyForSingleColumn(t *testing.T) {
	cat := buildFixture(t, committed("edge_cases"))

	single := columnOf(t, cat, "edge_parent", "code")
	if single.Key != "UNI" {
		t.Errorf("edge_parent.code : clé %q, attendu UNI", single.Key)
	}
	composite := columnOf(t, cat, "edge_types", "big")
	if composite.Key != "MUL" {
		t.Errorf("edge_types.big (première colonne d'un unique composite) : clé %q, attendu MUL", composite.Key)
	}
}

// TestZerofillPreserved vérifie qu'un attribut que le parseur n'inclut pas dans
// sa représentation de type n'est pas perdu pour autant.
func TestZerofillPreserved(t *testing.T) {
	cat := buildFixture(t, committed("edge_cases"))
	zero := columnOf(t, cat, "edge_types", "zero")
	if !strings.Contains(zero.Type, "zerofill") {
		t.Errorf("edge_types.zero : type %q, zerofill attendu", zero.Type)
	}
}

func columnOf(t *testing.T, cat *catalog.Catalog, table, column string) *catalog.Column {
	t.Helper()
	for _, tb := range cat.Tables {
		if tb.Name != table {
			continue
		}
		if c := tb.Column(column); c != nil {
			return c
		}
		t.Fatalf("colonne %s absente de %s", column, table)
	}
	t.Fatalf("table %s absente du catalogue", table)
	return nil
}

func buildFromSQL(t *testing.T, sql, dialectID string) *catalog.Catalog {
	t.Helper()
	d, err := dialect.Get(dialectID)
	if err != nil {
		t.Fatalf("dialecte %s : %v", dialectID, err)
	}
	parsed, err := ddl.Parse(sql)
	if err != nil {
		t.Fatalf("analyse SQL : %v", err)
	}
	cat, _ := catalog.Build(parsed.Statements, d, "inline.sql")
	return cat
}

// Une contrainte CHECK ne doit pas être enregistrée avec une expression vide :
// le parseur ne remplit pas Text() pour ces nœuds, et l'expression disparaissait
// silencieusement du catalogue comme du rapport.
func TestCheckConstraintKeepsItsExpression(t *testing.T) {
	cat := buildFixture(t, committed("edge_cases"))
	var checks []string
	for _, tb := range cat.Tables {
		for _, c := range tb.Checks {
			if strings.TrimSpace(c.Expr) == "" {
				t.Errorf("%s.%s : contrainte CHECK sans expression", tb.Name, c.Name)
			}
			checks = append(checks, c.Name+" "+c.Expr)
		}
	}
	if len(checks) == 0 {
		t.Fatal("la fixture déclare une contrainte CHECK : elle doit apparaître dans le catalogue")
	}
	t.Logf("contraintes CHECK lues : %v", checks)
}
