package main

import (
	"path/filepath"
	"testing"
)

// -h affichait l'aide puis se terminait avec le code 1 : une aide n'est pas une
// erreur, un script qui teste « mysql-schema-diagram -h && … » ne doit pas casser.
func TestHelpExitsSuccessfully(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		if err := run([]string{arg}); err != nil {
			t.Errorf("run(%q) = %v ; l'aide doit réussir", arg, err)
		}
	}
}

func TestListDialectsExitsSuccessfully(t *testing.T) {
	if err := run([]string{"-list-dialects"}); err != nil {
		t.Errorf("run(-list-dialects) = %v", err)
	}
}

func TestMissingInputIsAnError(t *testing.T) {
	if err := run([]string{"-o", t.TempDir()}); err == nil {
		t.Error("appeler le programme sans -input doit être une erreur")
	}
}

func TestUnknownFlagIsAnError(t *testing.T) {
	if err := run([]string{"-nawak"}); err == nil {
		t.Error("un drapeau inconnu doit être une erreur")
	}
}

// Un vrai passage de bout en bout, pour vérifier que la ligne de commande est
// bien câblée sur le pipeline.
func TestRunEndToEndFromCommandLine(t *testing.T) {
	dir := t.TempDir()
	fixture, err := filepath.Abs("../../testdata/oracle/edge_cases.sql")
	if err != nil {
		t.Fatalf("chemin de la fixture : %v", err)
	}
	if err := run([]string{"-i", fixture, "-o", dir, "-dialect", "mysql-5.7", "-quiet"}); err != nil {
		t.Fatalf("run a échoué : %v", err)
	}
	if err := run([]string{"-i", fixture, "-o", dir, "-no-html", "-no-svg", "-quiet"}); err != nil {
		t.Fatalf("run avec sorties désactivées a échoué : %v", err)
	}
}
