#!/usr/bin/env bash
# Vendore Gio et lui applique le correctif de compatibilité Wine, si nécessaire.
#
# Appelé par « make cross » (cible vendor-patched) et par GoReleaser avant ses
# builds, pour que les binaires publiés soient les mêmes que ceux qu'on teste.
# Idempotent : ne refait rien si l'arbre vendorié est déjà patché.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

PATCH=third_party/gio-v0.10.3-wine-compat.patch
TARGET=vendor/gioui.org/app/os_windows.go

if [ -f "$TARGET" ] && grep -q 'WINE FALLBACK' "$TARGET"; then
	echo "arbre vendorié déjà patché"
	exit 0
fi

echo "vendorage et application du correctif Wine"
go mod vendor
patch -p1 < "$PATCH"
