#!/usr/bin/env bash
# Régénère l'oracle information_schema d'une fixture, à partir d'un vrai serveur
# MariaDB jetable.
#
# L'oracle est la référence : il ne doit jamais être édité à la main. Si un test
# échoue, on corrige le code, ou on assume explicitement l'écart dans le test.
#
# Usage :
#   nix shell nixpkgs#mariadb_106 -c bash testdata/regenerate-oracle.sh [répertoire]
#
# Le répertoire par défaut est testdata/oracle (fixtures synthétiques commitées).
# Les dumps de bases réelles doivent être régénérés dans un répertoire HORS du
# dépôt, par exemple ~/.local/share/mysql-schema-diagram/oracle : un schéma de production
# n'a rien à faire dans un historique Git.
#
# Produit, pour chaque <répertoire>/<nom>.sql :
#   <nom>.columns.tsv       information_schema.COLUMNS
#   <nom>.indexes.tsv       information_schema.STATISTICS
#   <nom>.foreignkeys.tsv   information_schema.KEY_COLUMN_USAGE (contraintes déclarées)
set -euo pipefail

RACINE=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ORACLE="${1:-$RACINE/testdata/oracle}"
if [ ! -d "$ORACLE" ]; then
  echo "Répertoire absent, rien à régénérer : $ORACLE"
  exit 0
fi
# Forme portable (compgen et shopt ne sont pas garantis selon le shell invoqué).
set -- "$ORACLE"/*.sql
if [ ! -e "$1" ]; then
  echo "Aucune fixture .sql dans $ORACLE"
  exit 0
fi
TRAVAIL=$(mktemp -d)
SOCK="$TRAVAIL/mysql.sock"

nettoyage() {
  [ -n "${MYPID:-}" ] && kill "$MYPID" 2>/dev/null || true
  rm -rf "$TRAVAIL"
}
trap nettoyage EXIT

mariadb-install-db --datadir="$TRAVAIL/data" \
  --auth-root-authentication-method=normal --skip-test-db > "$TRAVAIL/install.log" 2>&1
mariadbd --datadir="$TRAVAIL/data" --socket="$SOCK" --skip-networking \
  --skip-grant-tables --pid-file="$TRAVAIL/mysqld.pid" > "$TRAVAIL/mysqld.log" 2>&1 &
MYPID=$!

for _ in $(seq 1 60); do
  mariadb --socket="$SOCK" -u root -e 'SELECT 1' >/dev/null 2>&1 && break
  sleep 1
done
mariadb --socket="$SOCK" -u root -e 'SELECT VERSION()' >/dev/null

for fixture in "$ORACLE"/*.sql; do
  nom=$(basename "$fixture" .sql)
  echo "== $nom"
  mariadb --socket="$SOCK" -u root -e "DROP DATABASE IF EXISTS oracle; CREATE DATABASE oracle CHARACTER SET utf8mb4;"
  if ! mariadb --socket="$SOCK" -u root oracle < "$fixture"; then
    echo "ÉCHEC du chargement de $fixture" >&2
    exit 1
  fi

  mariadb --socket="$SOCK" -u root --batch --raw oracle -e "
    SELECT table_name, ordinal_position, column_name, column_type, is_nullable,
           column_key, column_default, extra, character_set_name, collation_name,
           generation_expression, column_comment
    FROM information_schema.columns WHERE table_schema='oracle'
    ORDER BY table_name, ordinal_position;" > "$ORACLE/$nom.columns.tsv"

  mariadb --socket="$SOCK" -u root --batch --raw oracle -e "
    SELECT table_name, index_name, non_unique, seq_in_index, column_name,
           coalesce(sub_part, ''), index_type
    FROM information_schema.statistics WHERE table_schema='oracle'
    ORDER BY table_name, index_name, seq_in_index;" > "$ORACLE/$nom.indexes.tsv"

  mariadb --socket="$SOCK" -u root --batch --raw oracle -e "
    SELECT k.table_name, k.constraint_name, k.column_name,
           k.referenced_table_name, k.referenced_column_name
    FROM information_schema.key_column_usage k
    WHERE k.table_schema='oracle' AND k.referenced_table_name IS NOT NULL
    ORDER BY k.table_name, k.constraint_name, k.ordinal_position;" > "$ORACLE/$nom.foreignkeys.tsv"

  wc -l "$ORACLE/$nom".*.tsv | sed 's/^/   /'
done

echo "Oracle régénéré dans $ORACLE"
