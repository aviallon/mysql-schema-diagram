// Package infer déduit les relations qu'un dump ne déclare pas, à partir de la
// convention de nommage des colonnes.
//
// Un dump MySQL très courant ne contient aucune contrainte FOREIGN KEY :
// l'intégrité est portée par l'application, et les liens n'existent que sous
// forme de colonnes nommées d'après la clé cible (« id_catalogue »,
// « fk_id_catalogue »). Ces relations sont présentées comme déduites et
// jamais mélangées aux contraintes réellement écrites dans le fichier.
//
// Le principe est la précision avant le rappel : un lien inventé est plus
// coûteux qu'un lien manquant, car il n'est pas visible comme une erreur. Quand
// plusieurs tables candidates subsistent au même niveau de confiance, la
// relation est refusée et signalée plutôt que devinée.
package infer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aviallon/mysql-schema-diagram/internal/catalog"
)

// Niveaux de confiance d'un candidat, du plus fort au plus faible.
const (
	rankNone = iota
	// rankNameEquals : le nom de la colonne est exactement celui de la clé.
	rankNameEquals
	// rankPrefixNamesTable : « <préfixe>_<clé> », où le préfixe nomme la table.
	rankPrefixNamesTable
	// rankTableDerived : « id_<table> » ou « fk_id_<table> », la table cible se
	// lisant directement dans le nom de la colonne.
	rankTableDerived
)

// Infer renvoie les relations déduites, triées, sans doublon ni ambiguïté.
func Infer(cat *catalog.Catalog) []catalog.Relation {
	relations, _ := InferWithWarnings(cat)
	return relations
}

// InferWithWarnings renvoie en plus ce qui a été refusé, et pourquoi.
func InferWithWarnings(cat *catalog.Catalog) ([]catalog.Relation, []string) {
	targets := singleColumnKeys(cat)

	seen := map[catalog.Relation]bool{}
	var (
		out       []catalog.Relation
		warnings  []string
		ambiguous int
	)
	for _, t := range cat.Tables {
		declared := declaredColumns(t)
		for _, col := range t.Columns {
			if declared[strings.ToLower(col.Name)] {
				continue
			}
			best, rank := bestCandidates(col.Name, t.Name, targets)
			switch {
			case len(best) == 1:
				rel := catalog.Relation{
					ChildTable:   t.Name,
					ChildColumn:  col.Name,
					ParentTable:  best[0].table,
					ParentColumn: best[0].column,
				}
				if seen[rel] {
					continue
				}
				seen[rel] = true
				out = append(out, rel)
			case len(best) > 1:
				ambiguous++
				if ambiguous <= 10 {
					warnings = append(warnings, fmt.Sprintf(
						"relation ambiguë ignorée : %s.%s pourrait viser %s",
						t.Name, col.Name, describe(best)))
				}
			}
			_ = rank
		}
	}
	if ambiguous > 10 {
		warnings = append(warnings, fmt.Sprintf(
			"… et %d autres relations ambiguës ignorées", ambiguous-10))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ChildTable != out[j].ChildTable {
			return out[i].ChildTable < out[j].ChildTable
		}
		if out[i].ChildColumn != out[j].ChildColumn {
			return out[i].ChildColumn < out[j].ChildColumn
		}
		return out[i].ParentTable < out[j].ParentTable
	})
	return out, warnings
}

// bestCandidates renvoie les candidats du meilleur niveau de confiance. Un
// résultat de plus d'un élément est une ambiguïté, pas un choix.
func bestCandidates(childColumn, childTable string, targets []keyTarget) ([]keyTarget, int) {
	var best []keyTarget
	bestRank := rankNone
	for _, target := range targets {
		if target.table == childTable {
			continue
		}
		rank := rankOf(childColumn, target)
		switch {
		case rank == rankNone:
			continue
		case rank > bestRank:
			bestRank, best = rank, []keyTarget{target}
		case rank == bestRank:
			best = append(best, target)
		}
	}
	return best, bestRank
}

type keyTarget struct {
	table  string
	column string
}

// singleColumnKeys renvoie toutes les colonnes qui peuvent servir de cible :
// clé primaire ou index unique portant sur une seule colonne. Les clés
// composites sont écartées, faute de pouvoir être identifiées sans ambiguïté.
func singleColumnKeys(cat *catalog.Catalog) []keyTarget {
	var out []keyTarget
	for _, t := range cat.Tables {
		for _, ix := range t.Indexes {
			if ix.Kind != catalog.KindPrimary && ix.Kind != catalog.KindUnique {
				continue
			}
			if len(ix.Columns) != 1 {
				continue
			}
			out = append(out, keyTarget{table: t.Name, column: ix.Columns[0].Name})
		}
	}
	return out
}

// rankOf note la confiance d'un appariement colonne enfant / clé cible.
//
// Trois formes sont reconnues, toutes observées sur des bases réelles :
//
//   - « id_catalogue » dans une table « catalogue » → la table se lit dans le
//     nom de la colonne (le plus sûr) ;
//   - « fk_id_catalogue » → même chose après retrait du préfixe « fk_ » ;
//   - « parent_code » vers « edge_parent.code » → le préfixe nomme la table ;
//   - « id_catalogue » vers « catalogue.id_catalogue » → égalité de nom.
func rankOf(childColumn string, target keyTarget) int {
	child := strings.ToLower(childColumn)
	key := strings.ToLower(target.column)
	table := strings.ToLower(target.table)

	// Forme « fk_<clé> » ou « <clé> » : le nom de la clé est repris tel quel.
	if child == "fk_"+key || child == key {
		if token, ok := strings.CutPrefix(key, "id_"); ok && token != "" {
			if token == table {
				return rankTableDerived
			}
			return rankNameEquals
		}
		if looksLikeForeignKey(child) {
			return rankNameEquals
		}
		return rankNone
	}

	// Forme « <préfixe>_<clé> » : le préfixe doit désigner la table, et non
	// simplement en être une sous-chaîne. « client_prospect_id » ne doit pas se
	// raccrocher à une table « client » sous prétexte que le nom commence pareil.
	if head, ok := strings.CutSuffix(child, "_"+key); ok && head != "" && namesTable(head, table) {
		return rankPrefixNamesTable
	}
	return rankNone
}

// namesTable compare le préfixe d'une colonne au nom d'une table, sur les
// frontières de mots : égalité, ou l'un qui est le suffixe de l'autre précédé
// d'un « _ ». « parent » désigne « edge_parent » ; « client_prospect » ne
// désigne pas « client ».
func namesTable(prefix, table string) bool {
	if prefix == table {
		return true
	}
	return strings.HasSuffix(prefix, "_"+table) || strings.HasSuffix(table, "_"+prefix)
}

func declaredColumns(t *catalog.Table) map[string]bool {
	out := map[string]bool{}
	for _, fk := range t.ForeignKeys {
		for _, c := range fk.Columns {
			out[strings.ToLower(c)] = true
		}
	}
	return out
}

// looksLikeForeignKey écarte les noms trop génériques (« id », « nom ») pour ne
// garder que ceux qui désignent explicitement une clé.
func looksLikeForeignKey(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "id_"):
		return true
	case strings.HasPrefix(lower, "fk_"):
		return true
	case strings.HasSuffix(lower, "_id"):
		return true
	}
	return false
}

func describe(targets []keyTarget) string {
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		parts = append(parts, t.table+"."+t.column)
	}
	return strings.Join(parts, " ou ")
}
