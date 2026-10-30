# MySQL Schema Diagram — construction, vérification et livraison.
#
# Toutes les cibles se lancent dans « nix develop », sauf « oracle » qui ouvre
# un vrai MariaDB jetable pour régénérer la référence information_schema.
#
#   make test      go vet + go test (Go pur, aucune dépendance système)
#   make fmt       gofmt -l -w .
#   make lint      gofmt doit être vide + go vet
#   make build     binaires Linux dans dist/ (CLI en Go pur, GUI en cgo)
#   make cross     binaires Windows dans dist/ (Go pur, -H=windowsgui)
#   make gate      porte de staticité : refus des .exe important une DLL hôte
#   make verify    test + cross + gate (ce que la CI appelle)
#   make oracle    régénère testdata/oracle/*.tsv depuis MariaDB 10.6
#   make leakguard vérifie qu'aucun identifiant confidentiel n'est dans le dépôt
#   make clean     efface dist/

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

GO      ?= go
DIST    ?= dist
# Version injectée dans main.version : les deux binaires déclarent cette variable.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Paquets vérifiables sans cgo sur l'hôte. La GUI Linux exige cgo (X11, Wayland,
# Vulkan) et ne peut donc pas être construite ni testée en CGO_ENABLED=0 ; sa
# compilation est vérifiée en Go pur sous Windows — exactement la configuration
# que « make cross » produit et que « make gate » valide. Elle est en plus vetée
# ici par la passe GOOS=windows ci-dessous, qui couvre tout le module.
HOST_PKGS = $(shell CGO_ENABLED=0 $(GO) list ./... 2>/dev/null | grep -v '/cmd/mysql-schema-diagram-gui$$' || true)

.PHONY: help all test fmt lint build cross gate verify oracle leakguard wine-compat vendor-patched clean-vendor clean

help:
	@echo "MySQL Schema Diagram — cibles disponibles :"
	@echo "  test    go vet + go test"
	@echo "  fmt     gofmt -l -w ."
	@echo "  lint    gofmt (doit être vide) + go vet"
	@echo "  build   binaires Linux dans $(DIST)/"
	@echo "  cross   binaires Windows dans $(DIST)/"
	@echo "  gate    vérifie qu'aucun .exe n'importe de DLL hôte"
	@echo "  verify  test + cross + gate"
	@echo "  oracle  régénère testdata/oracle depuis MariaDB 10.6"
	@echo "  leakguard  refuse les identifiants d'un dump confidentiel"
	@echo "  wine-compat  GUI Windows seule, compatible Wine (incluse dans cross)"
	@echo "  clean-vendor efface l'arbre vendorié patché"
	@echo "  clean   efface $(DIST)/"

all: verify

# --- vérification ------------------------------------------------------------

test:
	set -euo pipefail; \
	echo "== go vet (tout le module, Go pur/Windows)"; \
	CGO_ENABLED=0 GOOS=windows $(GO) vet ./...; \
	echo "== go vet (hôte)"; \
	CGO_ENABLED=0 $(GO) vet $(HOST_PKGS); \
	echo "== go test"; \
	CGO_ENABLED=0 $(GO) test $(HOST_PKGS)

fmt:
	gofmt -l -w .

lint:
	set -euo pipefail; \
	unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt : fichiers à reformater :" >&2; \
		echo "$$unformatted" >&2; \
		exit 1; \
	fi; \
	CGO_ENABLED=0 GOOS=windows $(GO) vet ./...; \
	CGO_ENABLED=0 $(GO) vet $(HOST_PKGS)

# --- construction ------------------------------------------------------------

build:
	set -euo pipefail; \
	mkdir -p $(DIST); \
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/mysql-schema-diagram ./cmd/mysql-schema-diagram; \
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/mysql-schema-diagram-gui ./cmd/mysql-schema-diagram-gui

# Cross-compilation Windows : Gio n'exige pas cgo là-bas, les deux binaires sont
# donc construits en Go pur. « make gate » prouve qu'aucune DLL hôte n'est
# importée (kernel32.dll mis à part : c'est le noyau Windows, importé par le
# runtime Go lui-même, voir tools/peimports).
#
# La GUI part de l'arbre vendorié **patché** (voir « vendor-patched ») : sans le
# correctif, elle ne s'ouvre pas sous Wine, ce qui est l'usage courant de ces
# binaires. Le CLI, lui, n'en dépend pas.
cross: vendor-patched
	set -euo pipefail; \
	mkdir -p $(DIST); \
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/mysql-schema-diagram.exe ./cmd/mysql-schema-diagram; \
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS) -H=windowsgui' -o $(DIST)/mysql-schema-diagram-gui.exe ./cmd/mysql-schema-diagram-gui

# --- porte de staticité ------------------------------------------------------

gate:
	set -euo pipefail; \
	shopt -s nullglob; \
	exes=( $(DIST)/*.exe ); \
	if [ $${#exes[@]} -eq 0 ]; then \
		echo "$(DIST)/ ne contient aucun .exe : lancer « make cross » d'abord." >&2; \
		exit 1; \
	fi; \
	CGO_ENABLED=0 $(GO) run ./tools/peimports "$${exes[@]}"

verify: test cross gate leakguard

# Compatibilité Wine de la GUI Windows.
#
# Gio v0.10.3 traite EnableMouseInPointer comme fatal, alors que Wine n'en fournit
# qu'un stub (winehq bug 53847) : la fenêtre ne s'ouvre jamais. Le correctif tolère
# l'échec et traduit WM_MOUSE* en événements pointeur, sans quoi la souris reste
# inerte.
#
# C'est le chemin PAR DÉFAUT de « make cross », parce que ces binaires servent
# d'abord à être exécutés sous Wine. L'arbre vendorié est transitoire (vendor/ est
# ignoré par Git) et n'est reconstruit que s'il n'est pas déjà patché.
vendor-patched:
	@bash hack/vendor-patched.sh

# Alias explicite, pour construire la seule GUI.
wine-compat: vendor-patched
	set -euo pipefail; \
	mkdir -p $(DIST); \
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath \
	  -ldflags "-H=windowsgui $(LDFLAGS)" \
	  -o $(DIST)/mysql-schema-diagram-gui.exe ./cmd/mysql-schema-diagram-gui

# Efface l'arbre vendorié : les constructions suivantes repassent par le cache de
# modules, donc par Gio non patché — puis « make cross » le recrée.
clean-vendor:
	rm -rf vendor

# Vérifie qu'aucun identifiant des fixtures confidentielles (hors dépôt) ne s'est
# glissé dans les fichiers suivis. Sans fixture locale, la cible réussit sans rien
# vérifier : elle ne doit pas gêner un clone sans dump de production.
leakguard:
	set -euo pipefail; \
	CGO_ENABLED=0 $(GO) run ./tools/leakguard -allow tools/leakguard/allowlist.txt \
	  $$(git ls-files)

# --- oracle ------------------------------------------------------------------

# La référence information_schema est produite par un vrai serveur : elle fait
# foi. Un test qui échoue se corrige dans le code, ou s'assume explicitement
# dans internal/catalog/oracle_test.go — jamais en éditant les .tsv à la main.
#
# Deux répertoires sont traités : les fixtures synthétiques du dépôt, puis le
# répertoire local de dumps réels (hors dépôt, par construction).
oracle:
	set -euo pipefail; \
	nix shell nixpkgs#mariadb_106 -c bash testdata/regenerate-oracle.sh; \
	nix shell nixpkgs#mariadb_106 -c bash testdata/regenerate-oracle.sh \
	  "$${MYSQL_SCHEMA_DIAGRAM_ORACLE_DIR:-$$HOME/.local/share/mysql-schema-diagram/oracle}"

clean:
	rm -rf $(DIST)
