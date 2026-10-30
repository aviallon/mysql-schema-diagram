package ddl_test

import (
	"strings"
	"testing"

	"github.com/aviallon/mysql-schema-diagram/internal/ddl"
)

func TestStripExecutableComments(t *testing.T) {
	sql := "SET NAMES utf8;\n/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;\n" +
		"CREATE TABLE `t` (`a` int);"
	cleaned, warnings := ddl.StripExecutableComments(sql)
	if strings.Contains(cleaned, "40101") {
		t.Errorf("le commentaire exécutable n'a pas été retiré : %q", cleaned)
	}
	if len(warnings) != 0 {
		t.Errorf("un SET de session ne doit pas produire d'avertissement : %v", warnings)
	}
	if !strings.Contains(cleaned, "CREATE TABLE") {
		t.Errorf("du SQL utile a été perdu : %q", cleaned)
	}
}

// Un commentaire exécutable qui porte autre chose qu'un SET est signalé : c'est
// exactement le cas d'un trigger ou d'une routine, qu'on ne sait pas représenter.
func TestExecutableCommentWithCodeIsReported(t *testing.T) {
	sql := "/*!50003 CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW SET @a=1 */\nSELECT 1;"
	_, warnings := ddl.StripExecutableComments(sql)
	if len(warnings) == 0 {
		t.Fatal("un commentaire exécutable porteur de code doit produire un avertissement")
	}
	if !strings.Contains(warnings[0], "CREATE TRIGGER") {
		t.Errorf("l'avertissement ne cite pas le code ignoré : %v", warnings)
	}
}

func TestStripDelimiterBlocks(t *testing.T) {
	sql := "CREATE TABLE `t` (`a` int);\n" +
		"DELIMITER ;;\n" +
		"CREATE FUNCTION f() RETURNS int\nBEGIN\n  RETURN 1;\nEND ;;\n" +
		"DELIMITER ;\n" +
		"CREATE TABLE `u` (`b` int);\n"
	cleaned, warnings := ddl.StripDelimiterBlocks(sql)
	if strings.Contains(cleaned, "CREATE FUNCTION") {
		t.Errorf("le bloc DELIMITER n'a pas été retiré : %q", cleaned)
	}
	if !strings.Contains(cleaned, "CREATE TABLE `t`") || !strings.Contains(cleaned, "CREATE TABLE `u`") {
		t.Errorf("du SQL utile a été perdu autour du bloc : %q", cleaned)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "DELIMITER ;;") {
		t.Errorf("le retrait doit être signalé : %v", warnings)
	}
}

// Un bloc DELIMITER non terminé ne doit pas faire disparaître la suite du
// fichier en silence.
func TestUnterminatedDelimiterIsReported(t *testing.T) {
	sql := "CREATE TABLE `t` (`a` int);\nDELIMITER ;;\nBEGIN\n  RETURN 1;\n"
	cleaned, warnings := ddl.StripDelimiterBlocks(sql)
	if len(warnings) == 0 {
		t.Fatal("un bloc DELIMITER non terminé doit être signalé")
	}
	if !strings.Contains(cleaned, "CREATE TABLE `t`") {
		t.Errorf("le SQL valide avant le bloc a été perdu : %q", cleaned)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := ddl.Parse("CREATE TABL broken ("); err == nil {
		t.Error("un SQL invalide doit produire une erreur, pas un catalogue vide")
	}
}

// Un commentaire exécutable doit être RÉTABLI quand il porte une instruction
// qu'on sait lire : mysql et mariadb-dump encapsulent toutes les vues ainsi.
// Les supprimer en bloc faisait disparaître 100 % des vues d'un dump réel.
func TestExecutableCommentWithViewIsUnwrapped(t *testing.T) {
	sql := "/*!50001 CREATE VIEW `v_parent` AS SELECT `id` FROM `parent_table` */;"
	cleaned, warnings := ddl.StripExecutableComments(sql)
	if !strings.Contains(cleaned, "CREATE VIEW") {
		t.Fatalf("la vue a été supprimée au lieu d'être rétablie : %q", cleaned)
	}
	for _, w := range warnings {
		if strings.Contains(w, "CREATE VIEW") {
			t.Errorf("le rétablissement ne doit pas produire d'avertissement : %v", w)
		}
	}
	parsed, err := ddl.Parse(sql)
	if err != nil {
		t.Fatalf("analyse : %v", err)
	}
	if len(parsed.Statements) != 1 {
		t.Fatalf("%d instructions, attendu 1", len(parsed.Statements))
	}
}

// Ce qu'on ne sait pas représenter doit rester retiré ET signalé.
func TestExecutableCommentWithTriggerIsStillDropped(t *testing.T) {
	sql := "/*!50003 CREATE*/ /*!50017 DEFINER=`root`@`localhost`*/ /*!50003 TRIGGER `tr` BEFORE INSERT ON `x` FOR EACH ROW SET @a=1 */;"
	cleaned, warnings := ddl.StripExecutableComments(sql)
	if strings.Contains(cleaned, "TRIGGER") {
		t.Errorf("le trigger devrait être retiré : %q", cleaned)
	}
	if len(warnings) == 0 {
		t.Error("le retrait d'un trigger doit être signalé")
	}
}

// Les commentaires exécutables qu'un dump écrit autour d'ALTER TABLE ne doivent
// plus produire d'avertissement : ils sont rétablis et ignorés sans bruit.
func TestDumpBoilerplateProducesNoWarning(t *testing.T) {
	sql := "/*!40101 SET NAMES utf8mb4 */;\n" +
		"/*!40000 ALTER TABLE `t` DISABLE KEYS */;\n" +
		"/*!40000 ALTER TABLE `t` ENABLE KEYS */;"
	_, warnings := ddl.StripExecutableComments(sql)
	if len(warnings) != 0 {
		t.Errorf("le préambule d'un dump ne doit pas alerter : %v", warnings)
	}
}

// Un bloc DELIMITER dont le corps contient lui-même une ligne « delimiter »
// faisait paniquer le découpage (indice de tranche négatif).
func TestDelimiterBlockContainingDelimiterKeywordDoesNotPanic(t *testing.T) {
	// La ligne « delimiter ; » doit être seule sur sa ligne ET à l'intérieur du
	// bloc, sinon elle ne produit pas de correspondance et le défaut n'est pas
	// reproduit — un test qui passe dans les deux cas ne prouve rien.
	sql := "CREATE TABLE `t` (`a` int);\n" +
		"DELIMITER @@\n" +
		"CREATE FUNCTION f() RETURNS int\n" +
		"BEGIN\n" +
		"  SET @s = 'ligne un\n" +
		"delimiter ;\n" +
		"ligne trois';\n" +
		"  RETURN 1;\n" +
		"END @@\n" +
		"DELIMITER ;\n" +
		"CREATE TABLE `u` (`b` int);\n"

	var cleaned string
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("le découpage panique : %v", r)
			}
		}()
		cleaned, _ = ddl.StripDelimiterBlocks(sql)
	}()

	if !strings.Contains(cleaned, "CREATE TABLE `t`") || !strings.Contains(cleaned, "CREATE TABLE `u`") {
		t.Errorf("du SQL utile a été perdu : %q", cleaned)
	}
	if strings.Contains(cleaned, "CREATE FUNCTION") {
		t.Errorf("le bloc DELIMITER aurait dû être retiré : %q", cleaned)
	}
}
