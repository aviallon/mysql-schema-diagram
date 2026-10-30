// Command leakguard refuse qu'un identifiant d'un dump confidentiel se retrouve
// dans un fichier versionné.
//
// Le dépôt contient des fixtures synthétiques, jamais un schéma de production :
// les dumps réels vivent hors du dépôt ($MYSQL_SCHEMA_DIAGRAM_ORACLE_DIR). Ce garde
// vérifie que la séparation tient : il extrait les identifiants des fixtures
// confidentielles, puis les cherche dans les fichiers qu'on s'apprête à
// commiter.
//
// Sans fixture confidentielle, il ne fait rien et réussit : il ne doit pas gêner
// une personne qui clone le dépôt sans y déposer de dump.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// genericIdentifiers sont des noms de tables si courants qu'un document
// technique les emploie naturellement : les signaler produirait des faux
// positifs à chaque commit. Cette liste ne concerne que les noms de tables :
// voir « add » pour les colonnes.
var genericIdentifiers = map[string]bool{
	"article": true, "catalogue": true, "client": true, "commande": true,
	"parent": true, "enfant": true, "user": true, "users": true,
	"test": true, "data": true, "info": true, "list": true, "liste": true,
	"roles": true, "logs": true, "config": true, "settings": true,
	// Mots français qu'un commentaire technique emploie naturellement.
	"fonction": true, "mesure": true, "type": true, "etat": true, "temps": true,
	"service": true, "membre": true, "essai": true, "page": true, "signal": true,
	"document": true, "donnee": true, "donnees": true, "valeur": true,
}

var backticked = regexp.MustCompile("`([A-Za-z0-9_$]+)`")

// tableDeclaration repère les noms de tables sans ambiguïté : un backtick isolé
// peut aussi bien désigner une colonne ou un index.
var tableDeclaration = regexp.MustCompile("(?i)(?:CREATE|DROP|ALTER|RENAME)[ \t]+(?:TEMPORARY[ \t]+)?TABLE[ \t]+(?:IF[ \t]+(?:NOT[ \t]+)?EXISTS[ \t]+)?`([A-Za-z0-9_$]+)`")

func main() {
	secrets := flag.String("secrets", defaultSecretsDir(),
		"répertoire des fixtures confidentielles (hors dépôt)")
	allowFile := flag.String("allow", "",
		"fichier d'identifiants autorisés, un par ligne")
	quiet := flag.Bool("quiet", false, "ne rien afficher si tout va bien")
	flag.Parse()

	paths := flag.Args()

	allowed := map[string]bool{}
	if *allowFile != "" {
		if err := loadAllowlist(*allowFile, allowed); err != nil {
			fail(err)
		}
	}

	identifiers, err := collectIdentifiers(*secrets)
	if err != nil {
		fail(err)
	}
	if len(identifiers) == 0 {
		if !*quiet {
			fmt.Printf("leakguard : aucune fixture confidentielle dans %s — rien à vérifier\n", *secrets)
		}
		return
	}

	findings := scan(paths, identifiers, allowed)
	if len(findings) == 0 {
		if !*quiet {
			fmt.Printf("leakguard : %d identifiant(s) confidentiel(s) surveillé(s), aucun trouvé dans %d fichier(s)\n",
				len(identifiers), len(paths))
		}
		return
	}

	// Les identifiants sont affichés en clair : ce garde ne s'exécute que là où
	// les fixtures confidentielles existent, c'est-à-dire chez la personne qui
	// les détient déjà. Sans elles, il ne fait rien — rien ne fuit en CI.
	fmt.Fprintln(os.Stderr, "leakguard : identifiant(s) confidentiel(s) détecté(s)")
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "  %s:%d  %q dans : %s\n", f.path, f.line, f.identifier, strings.TrimSpace(f.text))
	}
	fmt.Fprintln(os.Stderr, "\nCes identifiants viennent des fixtures confidentielles de", *secrets)
	fmt.Fprintln(os.Stderr, "Retirez-les du contenu versionné. Pour un faux positif légitime,")
	fmt.Fprintln(os.Stderr, "ajoutez l'identifiant à tools/leakguard/allowlist.txt avec une justification.")
	os.Exit(1)
}

// defaultSecretsDir applique la même convention que les tests et le Makefile.
func defaultSecretsDir() string {
	if dir := os.Getenv("MYSQL_SCHEMA_DIAGRAM_ORACLE_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "mysql-schema-diagram", "oracle")
}

// watched est un identifiant confidentiel mis sous surveillance.
type watched struct {
	display string
	// tokens est l'identifiant découpé sur ses séparateurs.
	tokens []string
	// joined est l'identifiant recollé, pour reconnaître une écriture
	// camelCase : « id_commande » doit aussi matcher « idCommande ».
	joined string
}

type finding struct {
	path       string
	line       int
	identifier string
	text       string
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "leakguard :", err)
	os.Exit(2)
}

func loadAllowlist(path string, into map[string]bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("lecture de l'allowlist : %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		into[strings.Join(tokenize(line), " ")] = true
	}
	return scanner.Err()
}

// collectIdentifiers rassemble les noms de tables et de colonnes présents dans
// les fixtures confidentielles : le .sql, et les oracles .tsv produits par
// regenerate-oracle.sh.
func collectIdentifiers(dir string) ([]watched, error) {
	out := map[string]watched{}
	if dir == "" {
		return nil, nil
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		// Aucun dump local : il n'y a rien à protéger.
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("lecture de %s : %w", dir, err)
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case strings.HasSuffix(e.Name(), ".sql"):
			if err := collectFromSQL(path, out); err != nil {
				return nil, err
			}
		case strings.HasSuffix(e.Name(), ".columns.tsv"):
			if err := collectFromTSV(path, out, 2); err != nil {
				return nil, err
			}
		case strings.HasSuffix(e.Name(), ".indexes.tsv"):
			// information_schema.STATISTICS : table, index, non_unique,
			// seq_in_index, colonne.
			if err := collectFromTSV(path, out, 4); err != nil {
				return nil, err
			}
		}
	}

	list := make([]watched, 0, len(out))
	for _, w := range out {
		list = append(list, w)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].display < list[j].display })
	return list, nil
}

func collectFromSQL(path string, out map[string]watched) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", path, err)
	}
	text := string(data)

	// Les noms de tables d'abord : eux seuls portent l'empreinte du schéma, et
	// la déclaration les identifie sans ambiguïté.
	tables := map[string]bool{}
	for _, m := range tableDeclaration.FindAllStringSubmatch(text, -1) {
		if genericIdentifiers[strings.ToLower(m[1])] {
			continue
		}
		tables[strings.ToLower(m[1])] = true
		add(out, m[1])
	}
	// Tout le reste est un nom de colonne ou d'index : on ne retient que les
	// noms composés, un mot seul étant un mot courant.
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		if tables[strings.ToLower(m[1])] {
			continue
		}
		add(out, m[1])
	}
	return nil
}

// collectFromTSV lit l'oracle produit par regenerate-oracle.sh. columnIndex est
// la position du nom de colonne, qui diffère selon le fichier.
func collectFromTSV(path string, out map[string]watched, columnIndex int) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", path, err)
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.Comma = '\t'
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", path, err)
	}
	for i, row := range rows {
		if i == 0 || len(row) < 2 {
			continue // en-tête
		}
		add(out, row[0])
		if len(row) > columnIndex {
			add(out, row[columnIndex])
		}
	}
	return nil
}

// add met un identifiant sous surveillance.
//
// Un nom de table porte l'empreinte du schéma. Pour une colonne, on ne retient
// que les noms composés (« id_commande », « nom_client ») : un mot seul est un
// mot courant, sa présence dans un document technique ne signale rien, et le
// surveiller bloquerait chaque commit pour rien.
func add(out map[string]watched, identifier string) {
	identifier = strings.TrimSpace(identifier)
	tokens := tokenize(identifier)
	if len(tokens) == 0 || len(strings.Join(tokens, "")) < 4 {
		return
	}
	if len(tokens) == 1 && !strings.ContainsAny(identifier, "_-") {
		// Mot seul : trop courant pour être surveillé (voir genericIdentifiers
		// pour les noms de tables, qui passent par un autre chemin).
		return
	}
	joined := strings.Join(tokens, "")
	out[joined] = watched{display: identifier, tokens: tokens, joined: joined}
}

// tokenize découpe une chaîne sur tout ce qui n'est pas lettre ASCII ou chiffre.
//
// Les lettres accentuées ne sont **pas** repliées sur leur équivalent ASCII :
// « documentés » ne doit pas se confondre avec un identifiant « documents ».
func tokenize(s string) []string {
	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func scan(paths []string, identifiers []watched, allowed map[string]bool) []finding {
	var findings []finding
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() > 8<<20 {
			// Un fichier aussi gros n'est pas du texte versionnable utile.
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			tokens := tokenize(line)
			if len(tokens) == 0 {
				continue
			}
			for _, w := range identifiers {
				if allowed[strings.Join(w.tokens, " ")] || allowed[w.joined] {
					continue
				}
				if matchesIdentifier(tokens, w) {
					findings = append(findings, finding{
						path:       path,
						line:       i + 1,
						identifier: w.display,
						text:       line,
					})
					break // un signalement par ligne suffit
				}
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].path != findings[j].path {
			return findings[i].path < findings[j].path
		}
		return findings[i].line < findings[j].line
	})
	return findings
}

// matchesIdentifier compare un identifiant aux jetons d'une ligne.
//
// Deux formes suffisent, et elles évitent le faux positif par concaténation à
// travers la ponctuation (un identifiant « alpha_ref » ne doit pas se déclencher
// sur « contre (b,a) serait ») :
//
//   - les jetons de l'identifiant apparaissent consécutivement dans la ligne :
//     « id_commande », « id commande », « id-commande », « id.commande » ;
//   - les jetons recollés égalent un jeton de la ligne : « idCommande ».
func matchesIdentifier(lineTokens []string, w watched) bool {
	for _, token := range lineTokens {
		if token == w.joined {
			return true
		}
	}
	if len(w.tokens) > len(lineTokens) {
		return false
	}
	for start := 0; start+len(w.tokens) <= len(lineTokens); start++ {
		equal := true
		for i, want := range w.tokens {
			if lineTokens[start+i] != want {
				equal = false
				break
			}
		}
		if equal {
			return true
		}
	}
	return false
}
