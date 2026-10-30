package dialect_test

import (
	"testing"

	"github.com/aviallon/mysql-schema-diagram/internal/dialect"
)

func TestGetAcceptsCommonSpellings(t *testing.T) {
	for _, input := range []string{"mysql-5.7", "MySQL 5.7", "mysql5.7", "MYSQL_5.7", "mysql.5.7"} {
		d, err := dialect.Get(input)
		if err != nil {
			t.Errorf("Get(%q) a échoué : %v", input, err)
			continue
		}
		if d.ID != "mysql-5.7" {
			t.Errorf("Get(%q) = %q, attendu mysql-5.7", input, d.ID)
		}
	}
}

func TestGetRejectsUnknownServer(t *testing.T) {
	if _, err := dialect.Get("postgres-15"); err == nil {
		t.Error("un serveur non pris en charge doit être refusé")
	}
}

func TestDefaultCollation(t *testing.T) {
	cases := []struct {
		version string
		charset string
		want    string
	}{
		{"mysql-5.7", "utf8", "utf8_general_ci"},
		{"mysql-5.7", "utf8mb4", "utf8mb4_general_ci"},
		{"mysql-8.0", "utf8mb4", "utf8mb4_0900_ai_ci"},
		{"mysql-8.4", "utf8mb4", "utf8mb4_0900_ai_ci"},
		{"mariadb-10.6", "utf8mb4", "utf8mb4_general_ci"},
		{"mysql-5.7", "latin1", "latin1_swedish_ci"},
		{"mysql-5.7", "inexistant", ""},
	}
	for _, tc := range cases {
		d, err := dialect.Get(tc.version)
		if err != nil {
			t.Fatalf("dialecte %s : %v", tc.version, err)
		}
		if got := d.DefaultCollation(tc.charset); got != tc.want {
			t.Errorf("%s.DefaultCollation(%q) = %q, attendu %q", tc.version, tc.charset, got, tc.want)
		}
	}
}

// Chaque dialecte doit rester joignable par son label affiché dans la GUI,
// sinon l'interface et la ligne de commande divergeraient.
func TestEveryDialectIsResolvableByLabel(t *testing.T) {
	for _, d := range dialect.All() {
		byID, err := dialect.Get(d.ID)
		if err != nil || byID.ID != d.ID {
			t.Errorf("Get(%q) = %v, %v", d.ID, byID.ID, err)
		}
		byLabel, err := dialect.Get(d.Label)
		if err != nil || byLabel.ID != d.ID {
			t.Errorf("Get(label %q) = %v, %v", d.Label, byLabel.ID, err)
		}
		if d.Label == "" {
			t.Errorf("le dialecte %s n'a pas de libellé pour l'interface", d.ID)
		}
	}
}
