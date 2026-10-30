// Package ddl lit un fichier SQL et en extrait les instructions analysables.
//
// Aucune connexion à un serveur n'est faite : le texte est nettoyé des
// constructions propres aux dumps (commentaires exécutables, blocs DELIMITER)
// puis confié au parseur TiDB.
package ddl

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	_ "github.com/pingcap/tidb/pkg/parser/test_driver" // enregistre le driver d'expressions
)

// Result est le fruit de l'analyse d'un fichier.
type Result struct {
	// Statements suit l'ordre du fichier.
	Statements []ast.StmtNode
	// Warnings liste ce qui a été retiré du texte avant analyse.
	Warnings []string
}

// ReadFile lit path et délègue à Parse.
func ReadFile(path string) (*Result, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", path, err)
	}
	return Parse(string(raw))
}

// Parse nettoie puis analyse le SQL fourni.
func Parse(sql string) (*Result, error) {
	cleaned, warnings := StripExecutableComments(sql)
	cleaned, moreWarnings := StripDelimiterBlocks(cleaned)
	warnings = append(warnings, moreWarnings...)

	p := parser.New()
	stmts, parseWarnings, err := p.Parse(cleaned, "", "")
	if err != nil {
		return nil, fmt.Errorf("analyse SQL : %w", err)
	}
	for i, w := range parseWarnings {
		if i >= 20 {
			warnings = append(warnings, fmt.Sprintf("… et %d autres avertissements du parseur", len(parseWarnings)-i))
			break
		}
		warnings = append(warnings, "parseur : "+w.Error())
	}
	return &Result{Statements: stmts, Warnings: warnings}, nil
}

// execComment capture le numéro de version et le corps d'un commentaire
// exécutable MySQL : /*!50001 <SQL> */.
var execComment = regexp.MustCompile(`(?s)/\*!([0-9]{0,6})(.*?)\*/`)

// unwrappable liste les débuts d'instruction qu'on sait représenter.
//
// mysql et mariadb-dump encapsulent les vues, les triggers et les routines dans
// des commentaires exécutables. Supprimer ces commentaires en bloc faisait
// disparaître **toutes** les vues d'un dump réel : on remet donc le corps dans
// le flux pour ce qu'on sait lire, et on ne retire (en le signalant) que ce
// qu'on ne sait pas représenter.
var unwrappable = []string{
	"SET ", "CREATE VIEW", "CREATE OR REPLACE VIEW", "CREATE TABLE",
	"CREATE INDEX", "CREATE UNIQUE INDEX", "ALTER TABLE", "DROP TABLE",
	"DROP VIEW", "INSERT INTO", "DELETE FROM", "UPDATE ",
}

// StripExecutableComments rétablit le corps des commentaires exécutables
// représentables et retire les autres, en les signalant.
func StripExecutableComments(sql string) (string, []string) {
	var warnings []string
	seen := map[string]bool{}
	dropped := 0

	out := execComment.ReplaceAllStringFunc(sql, func(match string) string {
		parts := execComment.FindStringSubmatch(match)
		body := strings.TrimSpace(parts[2])
		if body == "" {
			return " "
		}
		if isUnwrappable(body) {
			return " " + body + " "
		}
		dropped++
		head := body
		if len(head) > 60 {
			head = head[:60] + "…"
		}
		if !seen[head] {
			seen[head] = true
			warnings = append(warnings, "code non représenté, ignoré : "+head)
		}
		return " "
	})

	if dropped > len(seen) {
		warnings = append(warnings, fmt.Sprintf("… et %d autres blocs de code non représentés ignorés", dropped-len(seen)))
	}
	return out, warnings
}

func isUnwrappable(body string) bool {
	upper := strings.ToUpper(body)
	for _, prefix := range unwrappable {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

var delimiterLine = regexp.MustCompile(`(?im)^[ \t]*delimiter[ \t]+(\S+)[ \t]*\r?$`)

// StripDelimiterBlocks retire les blocs délimités par DELIMITER (définitions de
// routines et de triggers). Ces objets ne font pas partie du schéma physique des
// tables ; leur retrait évite de faire échouer l'analyse.
func StripDelimiterBlocks(sql string) (string, []string) {
	locs := delimiterLine.FindAllStringSubmatchIndex(sql, -1)
	if len(locs) == 0 {
		return sql, nil
	}

	var (
		out      strings.Builder
		warnings []string
		cursor   int
	)
	for _, loc := range locs {
		// Un bloc DELIMITER peut contenir une ligne « delimiter » dans le corps
		// d'une routine : cette occurrence est déjà passée, on l'ignore.
		if loc[0] < cursor {
			continue
		}
		delim := sql[loc[2]:loc[3]]
		bodyStart := loc[1]
		out.WriteString(sql[cursor:loc[0]])

		if delim == ";" {
			// Retour au délimiteur par défaut : rien à retirer.
			cursor = bodyStart
			continue
		}

		// Fin du bloc : première occurrence du délimiteur après son en-tête. Le
		// client mysql accepte le délimiteur n'importe où, et un dump écrit
		// couramment « END ;; » sur la même ligne que la fin du corps.
		offset := strings.Index(sql[bodyStart:], delim)
		if offset < 0 {
			warnings = append(warnings, fmt.Sprintf(
				"bloc DELIMITER %s non terminé : %d octets ignorés en fin de fichier",
				delim, len(sql)-bodyStart))
			cursor = len(sql)
			break
		}
		end := bodyStart + offset + len(delim)
		for end < len(sql) && sql[end] != '\n' {
			end++
		}
		warnings = append(warnings, fmt.Sprintf(
			"bloc DELIMITER %s ignoré (routine ou trigger, %d octets)", delim, end-bodyStart))
		cursor = end
	}
	out.WriteString(sql[cursor:])
	return out.String(), warnings
}
