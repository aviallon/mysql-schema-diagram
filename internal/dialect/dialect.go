// Package dialect décrit les différences observables entre les grands serveurs
// MySQL/MariaDB, du point de vue de la lecture d'un dump de schéma.
//
// Chaque différence listée ici a été vérifiée sur un serveur réel : voir
// testdata/ pour les fixtures et leur oracle information_schema.
package dialect

import (
	"fmt"
	"sort"
	"strings"
)

// Dialect regroupe les règles qui font qu'un même DDL n'est pas interprété de
// la même façon selon la version du serveur visé.
type Dialect struct {
	// ID est l'identifiant de ligne de commande, par exemple "mysql-5.7".
	ID string
	// Label est le nom affiché à l'utilisateur.
	Label string

	// ImplicitFirstTimestamp reproduit le comportement de
	// explicit_defaults_for_timestamp=OFF (valeur par défaut jusqu'à MySQL 5.7
	// inclus, et toujours le défaut de MariaDB) : la première colonne TIMESTAMP
	// d'une table qui n'a ni DEFAULT ni ON UPDATE explicites, et qui n'est pas
	// déclarée NULL, reçoit automatiquement
	// DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP.
	ImplicitFirstTimestamp bool

	// KeepIntegerDisplayWidth indique si le serveur conserve les largeurs
	// d'affichage des entiers dans information_schema.COLUMN_TYPE.
	// MySQL a déprécié puis ignoré ces largeurs à partir de 8.0.19 ;
	// MariaDB les conserve.
	KeepIntegerDisplayWidth bool

	// UTF8IsUTF8MB3 indique si le serveur renomme l'alias "utf8" en "utf8mb3"
	// dans CHARACTER_SET_NAME (MySQL >= 8.0.29, MariaDB >= 10.6).
	UTF8IsUTF8MB3 bool

	// TextBlobDefaultForbidden indique que DEFAULT est refusé sur TEXT/BLOB
	// (MySQL < 8.0.13).
	TextBlobDefaultForbidden bool

	// CheckEnforced indique que les contraintes CHECK sont réellement
	// appliquées (MySQL >= 8.0.16, MariaDB >= 10.2.1). MySQL 5.7 les accepte
	// syntaxiquement mais les ignore.
	CheckEnforced bool

	// UTF8MB4DefaultCollation0900 indique que la collation par défaut du jeu
	// utf8mb4 est utf8mb4_0900_ai_ci (MySQL >= 8.0) et non utf8mb4_general_ci.
	UTF8MB4DefaultCollation0900 bool
}

// DefaultCollation rend la collation que le serveur applique quand seule la
// table ou la colonne est déclarée, sans COLLATE explicite.
//
// C'est cette valeur — et non une chaîne vide — qu'expose
// information_schema.COLUMNS.COLLATION_NAME.
func (d Dialect) DefaultCollation(charset string) string {
	name := strings.ToLower(strings.TrimSpace(charset))
	if name == "" {
		return ""
	}
	if name == "utf8mb4" && d.UTF8MB4DefaultCollation0900 {
		return "utf8mb4_0900_ai_ci"
	}
	if c, ok := defaultCollations[name]; ok {
		return c
	}
	return ""
}

// defaultCollations reprend la collation par défaut de chaque jeu de caractères
// livré avec MySQL 5.7 / MariaDB (information_schema.COLLATIONS, ID le plus bas).
var defaultCollations = map[string]string{
	"armscii8": "armscii8_general_ci",
	"ascii":    "ascii_general_ci",
	"big5":     "big5_chinese_ci",
	"binary":   "binary",
	"cp1250":   "cp1250_general_ci",
	"cp1251":   "cp1251_general_ci",
	"cp1256":   "cp1256_general_ci",
	"cp1257":   "cp1257_general_ci",
	"cp850":    "cp850_general_ci",
	"cp852":    "cp852_general_ci",
	"cp866":    "cp866_general_ci",
	"cp932":    "cp932_japanese_ci",
	"dec8":     "dec8_swedish_ci",
	"eucjpms":  "eucjpms_japanese_ci",
	"euckr":    "euckr_korean_ci",
	"gb18030":  "gb18030_chinese_ci",
	"gb2312":   "gb2312_chinese_ci",
	"gbk":      "gbk_chinese_ci",
	"geostd8":  "geostd8_general_ci",
	"greek":    "greek_general_ci",
	"hebrew":   "hebrew_general_ci",
	"hp8":      "hp8_english_ci",
	"keybcs2":  "keybcs2_general_ci",
	"koi8r":    "koi8r_general_ci",
	"koi8u":    "koi8u_general_ci",
	"latin1":   "latin1_swedish_ci",
	"latin2":   "latin2_general_ci",
	"latin5":   "latin5_turkish_ci",
	"latin7":   "latin7_general_ci",
	"macce":    "macce_general_ci",
	"macroman": "macroman_general_ci",
	"sjis":     "sjis_japanese_ci",
	"swe7":     "swe7_swedish_ci",
	"tis620":   "tis620_thai_ci",
	"ucs2":     "ucs2_general_ci",
	"ujis":     "ujis_japanese_ci",
	"utf8":     "utf8_general_ci",
	"utf8mb3":  "utf8mb3_general_ci",
	"utf8mb4":  "utf8mb4_general_ci",
	"utf16":    "utf16_general_ci",
	"utf16le":  "utf16le_general_ci",
	"utf32":    "utf32_general_ci",
}

var all = []Dialect{
	{
		ID:                       "mysql-5.7",
		Label:                    "MySQL 5.7",
		ImplicitFirstTimestamp:   true,
		KeepIntegerDisplayWidth:  true,
		UTF8IsUTF8MB3:            false,
		TextBlobDefaultForbidden: true,
		CheckEnforced:            false,
	},
	{
		ID:                          "mysql-8.0",
		Label:                       "MySQL 8.0",
		ImplicitFirstTimestamp:      false,
		KeepIntegerDisplayWidth:     false,
		UTF8IsUTF8MB3:               true,
		TextBlobDefaultForbidden:    false,
		CheckEnforced:               true,
		UTF8MB4DefaultCollation0900: true,
	},
	{
		ID:                          "mysql-8.4",
		Label:                       "MySQL 8.4",
		ImplicitFirstTimestamp:      false,
		KeepIntegerDisplayWidth:     false,
		UTF8IsUTF8MB3:               true,
		TextBlobDefaultForbidden:    false,
		CheckEnforced:               true,
		UTF8MB4DefaultCollation0900: true,
	},
	{
		ID:                       "mariadb-10.6",
		Label:                    "MariaDB 10.6",
		ImplicitFirstTimestamp:   true,
		KeepIntegerDisplayWidth:  true,
		UTF8IsUTF8MB3:            true,
		TextBlobDefaultForbidden: false,
		CheckEnforced:            true,
	},
	{
		ID:                       "mariadb-11.4",
		Label:                    "MariaDB 11.4",
		ImplicitFirstTimestamp:   true,
		KeepIntegerDisplayWidth:  true,
		UTF8IsUTF8MB3:            true,
		TextBlobDefaultForbidden: false,
		CheckEnforced:            true,
	},
}

// DefaultID est le dialecte utilisé si l'utilisateur n'en choisit aucun.
const DefaultID = "mysql-5.7"

// All renvoie tous les dialectes connus, dans l'ordre d'affichage.
func All() []Dialect {
	out := make([]Dialect, len(all))
	copy(out, all)
	return out
}

// IDs renvoie la liste triée des identifiants acceptés (pour les messages d'erreur).
func IDs() []string {
	ids := make([]string, 0, len(all))
	for _, d := range all {
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return ids
}

// Get résout un identifiant de dialecte. La casse et les séparateurs sont
// normalisés : "MySQL 5.7", "mysql5.7" et "mysql-5.7" sont équivalents.
func Get(id string) (Dialect, error) {
	norm := normalize(id)
	for _, d := range all {
		if normalize(d.ID) == norm || normalize(d.Label) == norm {
			return d, nil
		}
	}
	return Dialect{}, fmt.Errorf("version de serveur inconnue %q (valeurs acceptées : %s)",
		id, strings.Join(IDs(), ", "))
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "")
	return replacer.Replace(s)
}
