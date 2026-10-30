#!/bin/sh
# GUI Linux statique : Wayland + Vulkan, musl, cgo — dans un conteneur Alpine.
#
#   docker run --rm -v $PWD:/src:ro -v /tmp/out:/out \
#     -v $PWD/hack/build-linux-static-alpine.sh:/build.sh:ro \
#     alpine:3.21 sh /build.sh
#
# ATTENTION : le binaire obtenu est bien statique (aucune section .dynamic),
# mais il NE FONCTIONNE PAS. Gio charge le chargeur Vulkan par dlopen, et un
# binaire musl pleinement statique ne sait pas faire de dlopen — il sort en
# code 0 sans ouvrir de fenêtre. Voir docs/linux-gui-statique.md : ce script
# reproduit la démonstration, il ne produit pas un livrable.
set -e
export GOTOOLCHAIN=auto
export GOFLAGS=-mod=vendor
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

apk add --no-cache go gcc musl-dev make pkgconf meson ninja curl tar xz \
  libffi-dev wayland-protocols linux-headers expat-dev cmake libxml2-dev bison vulkan-headers >/dev/null

fetch() {
  curl -fsSL "$1" -o /tmp/lib.tar
  rm -rf /tmp/src && mkdir /tmp/src
  case "$1" in
    *.tar.gz) tar -xzf /tmp/lib.tar -C /tmp/src --strip-components=1 ;;
    *.tar.xz) tar -xJf /tmp/lib.tar -C /tmp/src --strip-components=1 ;;
  esac
}
build_lib() {
  name=$1; url=$2; shift 2
  echo "=== $name ==="
  fetch "$url"
  cd /tmp/src
  meson setup build --prefix=/usr --default-library=static "$@" >/tmp/$name-meson.log 2>&1 || { tail -15 /tmp/$name-meson.log; exit 1; }
  ninja -C build >/tmp/$name-ninja.log 2>&1 || { tail -15 /tmp/$name-ninja.log; exit 1; }
  ninja -C build install >/dev/null
}
build_lib wayland https://gitlab.freedesktop.org/wayland/wayland/-/releases/1.23.1/downloads/wayland-1.23.1.tar.xz \
  -Dtests=false -Ddocumentation=false
build_lib xkbcommon https://github.com/xkbcommon/libxkbcommon/archive/refs/tags/xkbcommon-1.7.0.tar.gz \
  -Denable-x11=false -Denable-wayland=false -Denable-xkbregistry=false -Denable-docs=false

echo "=== binaire Go (Wayland + Vulkan, statique) ==="
cd /src
CGO_ENABLED=1 go build -tags "nox11 noopengl" -trimpath \
  -ldflags '-linkmode external -extldflags "-static"' \
  -o /out/mysql-schema-diagram-gui ./cmd/mysql-schema-diagram-gui
echo "--- file ---"; file /out/mysql-schema-diagram-gui
echo "--- ldd ---"; ldd /out/mysql-schema-diagram-gui 2>&1 | head -3
echo "--- taille ---"; ls -la /out/mysql-schema-diagram-gui
