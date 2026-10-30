package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lookup(identifiers []watched, name string) bool {
	for _, w := range identifiers {
		if w.joined == strings.Join(tokenize(name), "") {
			return true
		}
	}
	return false
}

// Un garde qui ne peut pas échouer ne prouve rien : ce test construit une
// fixture « confidentielle » synthétique et vérifie que le garde la détecte,
// y compris quand l'identifiant est écrit autrement (casse, séparateurs).
func TestGuardDetectsIdentifiersFromSecretFixtures(t *testing.T) {
	secrets := t.TempDir()
	work := t.TempDir()

	writeFile(t, filepath.Join(secrets, "production.sql"),
		"CREATE TABLE `zorglub_bench` (\n  `id_zorglub_bench` int NOT NULL,\n  `fk_id_zorglub_bench` int DEFAULT NULL\n);\n")
	writeFile(t, filepath.Join(secrets, "production.columns.tsv"),
		"table_name\tordinal_position\tcolumn_name\nzorglub_bench\t1\tid_zorglub_bench\n")

	identifiers, err := collectIdentifiers(secrets)
	if err != nil {
		t.Fatalf("extraction : %v", err)
	}
	if len(identifiers) == 0 {
		t.Fatal("aucun identifiant extrait : le garde ne surveillerait rien")
	}

	// Cas innocent : rien à signaler.
	clean := filepath.Join(work, "propre.go")
	writeFile(t, clean, "// un commentaire sans rapport avec une base\n")
	if findings := scan([]string{clean}, identifiers, nil); len(findings) != 0 {
		t.Errorf("faux positif sur un fichier propre : %+v", findings)
	}

	// Cas fautif, sous quatre écritures de surface différentes.
	for name, content := range map[string]string{
		"brut.md":     "relation : zorglub_bench.id_zorglub_bench\n",
		"casse.md":    "relation : ZORGLUB_BENCH.id_zorglub_bench\n",
		"tirets.md":   "relation : id-zorglub-bench\n",
		"sans_sep.md": "relation : idZorglubBench\n",
	} {
		path := filepath.Join(work, name)
		writeFile(t, path, content)
		if findings := scan([]string{path}, identifiers, nil); len(findings) == 0 {
			t.Errorf("%s : identifiant confidentiel non détecté", name)
		}
	}
}

// Une colonne d'un seul mot est un mot courant : la surveiller ferait échouer
// chaque commit sur « fonction » ou « mesure ».
func TestSingleWordColumnsAreNotWatched(t *testing.T) {
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "production.columns.tsv"),
		"table_name\tordinal_position\tcolumn_name\nzorglub_bench\t1\tfonction\nzorglub_bench\t2\tid_zorglub\n")
	identifiers, err := collectIdentifiers(secrets)
	if err != nil {
		t.Fatalf("extraction : %v", err)
	}
	if lookup(identifiers, "fonction") {
		t.Error("une colonne d'un seul mot ne doit pas être surveillée")
	}
	if !lookup(identifiers, "id_zorglub") {
		t.Error("une colonne composée doit être surveillée")
	}
	if !lookup(identifiers, "zorglub_bench") {
		t.Error("un nom de table doit être surveillé")
	}
}

// Les noms trop génériques sont ignorés : les signaler bloquerait chaque commit
// sur des mots comme « commentaire » ou « valeur ».
func TestGenericIdentifiersAreIgnored(t *testing.T) {
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "production.columns.tsv"),
		"table_name\tordinal_position\tcolumn_name\nclient\t1\tcommentaire\nclient\t2\tid\n")
	identifiers, err := collectIdentifiers(secrets)
	if err != nil {
		t.Fatalf("extraction : %v", err)
	}
	for _, w := range identifiers {
		if w.display == "commentaire" || w.display == "id" || w.display == "client" {
			t.Errorf("%q ne devrait pas être surveillé", w.display)
		}
	}
}

// Absence de fixture confidentielle : le garde doit réussir sans rien signaler,
// sinon un simple clone du dépôt ne pourrait plus commiter.
func TestNoSecretFixturesMeansNoOp(t *testing.T) {
	identifiers, err := collectIdentifiers(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("répertoire absent : %v", err)
	}
	if len(identifiers) != 0 {
		t.Errorf("un répertoire absent doit donner zéro identifiant, obtenu %d", len(identifiers))
	}
}

// L'allowlist doit réellement neutraliser un identifiant : c'est le seul moyen
// d'assumer un faux positif sans désactiver le garde.
func TestAllowlistSuppressesAFinding(t *testing.T) {
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "production.sql"), "CREATE TABLE `zorglub_bench` (`a` int);\n")
	identifiers, err := collectIdentifiers(secrets)
	if err != nil {
		t.Fatalf("extraction : %v", err)
	}
	work := t.TempDir()
	path := filepath.Join(work, "fuite.md")
	writeFile(t, path, "table zorglub_bench\n")

	allowed := map[string]bool{strings.Join(tokenize("zorglub_bench"), " "): true}
	if findings := scan([]string{path}, identifiers, allowed); len(findings) != 0 {
		t.Errorf("l'allowlist n'a pas neutralisé le signalement : %+v", findings)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("écriture de %s : %v", path, err)
	}
}

// Un identifiant ne doit pas se déclencher à travers la ponctuation : c'est le
// faux positif qui rendait le garde inutilisable (« alpha_ref » sur une phrase
// contenant « (b,a) serait »).
func TestNoFalsePositiveAcrossPunctuation(t *testing.T) {
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "production.sql"),
		"CREATE TABLE `zorglub_bench` (`ref` int);\n")
	identifiers, err := collectIdentifiers(secrets)
	if err != nil {
		t.Fatalf("extraction : %v", err)
	}
	work := t.TempDir()
	path := filepath.Join(work, "phrase.md")
	writeFile(t, path, "Un index `(b,a)` serait passé | comparaison position par position\n")
	if findings := scan([]string{path}, identifiers, nil); len(findings) != 0 {
		t.Errorf("faux positif à travers la ponctuation : %+v", findings)
	}
}

// Les accents ne sont pas repliés : « documentés » ne doit pas déclencher un
// identifiant « documents ».
func TestAccentsDoNotCollapseOntoIdentifiers(t *testing.T) {
	secrets := t.TempDir()
	writeFile(t, filepath.Join(secrets, "production.columns.tsv"),
		"table_name\tordinal_position\tcolumn_name\ngeneve_docs\t1\tcode_gen\n")
	identifiers, err := collectIdentifiers(secrets)
	if err != nil {
		t.Fatalf("extraction : %v", err)
	}
	work := t.TempDir()
	path := filepath.Join(work, "phrases.md")
	writeFile(t, path, "des documents sont générés, et la mesure est passée\n")
	if findings := scan([]string{path}, identifiers, nil); len(findings) != 0 {
		t.Errorf("faux positif accentué : %+v", findings)
	}
}
