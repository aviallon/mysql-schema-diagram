#!/usr/bin/env bash
# Vendore Gio et lui applique le correctif de compatibilité Wine, si nécessaire.
#
# Appelé par « make cross » (cible vendor-patched) et par GoReleaser avant ses
# builds, pour que les binaires publiés soient les mêmes que ceux qu'on teste.
# Idempotent : ne refait rien si l'arbre vendorié est déjà à jour et patché.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

PATCH=third_party/gio-v0.10.3-wine-compat.patch
TARGET=vendor/gioui.org/app/os_windows.go

# go.mod/go.sum plus récents que le vendorage : une dépendance a bougé, l'arbre
# est périmé et le build échouerait sur un modules.txt incohérent.
if [ ! -f "$TARGET" ] || [ go.mod -nt vendor/modules.txt ] || [ go.sum -nt vendor/modules.txt ]; then
	echo "vendorage"
	go mod vendor
fi

if ! grep -q 'WINE FALLBACK' "$TARGET"; then
	echo "application du correctif Wine"
	patch -p1 < "$PATCH"
fi
