# GUI Linux statique : ce qui marche, ce qui bloque

Objectif : produire la GUI Linux en **un seul binaire statique**, comme Alpine le
fait pour ses paquets — c'est-à-dire musl + cgo, et non l'approche glibc où la
liaison statique traîne ses problèmes de NSS.

**Résultat : la liaison statique fonctionne ; le binaire ne peut pas s'exécuter.**
Et la cause est précise, vérifiée par une expérience minimale.

## Ce qui est acquis

Gio se compile en **Wayland + Vulkan** avec `-tags "nox11 noopengl"` (les
contraintes de compilation de `gioui.org/app` le permettent : `!nox11`,
`!nowayland`, `!noopengl`). Dans cette configuration, il ne reste à lier que
`wayland-client`, `wayland-cursor`, `wayland-egl` et `xkbcommon` : Alpine les
fournit en version statique (`wayland-static`, `libxkbcommon-static`), et
`vulkan-headers` suffit puisque le chargeur Vulkan est `dlopen`é.

```dockerfile
FROM alpine:3.21
RUN apk add --no-cache go gcc musl-dev pkgconf \
      wayland-static wayland-dev libxkbcommon-static libxkbcommon-dev \
      vulkan-headers
```

```console
$ CGO_ENABLED=1 go build -tags "nox11 noopengl" -trimpath \
    -ldflags '-linkmode external -extldflags "-static"' \
    -o mysql-schema-diagram-gui ./cmd/mysql-schema-diagram-gui
$ file mysql-schema-diagram-gui
mysql-schema-diagram-gui: ELF 64-bit LSB executable, x86-64, statically linked
$ readelf -d mysql-schema-diagram-gui
Il n'y a pas de section dynamique dans ce fichier.
```

Le binaire est donc **réellement** statique — pas un « presque statique » avec un
`ld.so` caché. (`hack/build-linux-static-alpine.sh` reproduit la construction.)

## Ce qui bloque

Il se lance, puis **sort en code 0 sans jamais ouvrir de fenêtre**.

La raison tient en une phrase : **`gioui.org/internal/vk` charge le chargeur
Vulkan par `dlopen`, et un binaire musl entièrement statique ne peut pas faire de
`dlopen` du tout.** Sans chargeur, pas de périphérique Vulkan, donc pas de
contexte GPU, donc pas de fenêtre — et Gio échoue sans rien dire.

L'expérience minimale qui l'établit, en six lignes de C :

```c
#include <dlfcn.h>
#include <stdio.h>
int main(void) {
    printf("dlopen -> %s\n", dlopen("libvulkan.so.1", RTLD_NOW) ? "OK" : dlerror());
    return 0;
}
```

```console
$ gcc -static -o t-static t.c -ldl && ./t-static
dlopen -> Dynamic loading not supported
$ gcc -static-pie -o t-pie t.c -ldl && ./t-pie
dlopen -> Dynamic loading not supported
```

C'est une limite documentée de musl : un binaire statique n'embarque pas de
chargeur dynamique. `-static-pie`, qui laisse pourtant croire le contraire, ne
change rien — vérifié aussi.

## Les trois issues possibles

| Voie | Ce qu'elle demande | Ce qu'elle donne |
| --- | --- | --- |
| **A. Lier Vulkan au lieu de le charger** | Construire le chargeur Vulkan en statique (`-DBUILD_STATIC_LOADER=ON`) **et** modifier `gioui.org/internal/vk` pour référencer les symboles directement au lieu de passer par `dlopen` | Binaire statique complet, Wayland + Vulkan. Le patch Gio est plus lourd que celui de Wine : le fichier fait ~700 lignes de liaisons générées |
| **B. Passer par OpenGL/EGL** | Retirer `noopengl` : Gio lie alors `libEGL` à la construction, donc la liaison statique suffit — mais il faut une **Mesa statique**, qu'Alpine ne fournit pas : c'est une construction lourde (LLVM) | Binaire statique complet, Wayland + EGL |
| **C. Rester dynamique** | Rien | Le binaire actuel : fonctionne, portable entre distributions NixOS… pas entre toutes, et ce n'est pas un binaire unique |

Aucune de ces voies n'est gratuite, et c'est le sens de ce document : « on peut
faire du statique avec cgo » est vrai pour **lier**, cela n'est pas vrai pour
**charger à chaud** un pilote sans chargeur dynamique.

## Ce qui n'a pas été testé

- La voie A (chargeur Vulkan statique + patch de `internal/vk`) : plausible, mais
  le patch est nettement plus lourd que celui déjà appliqué pour Wine.
- La voie B (Mesa statique) : coût de construction estimé, pas mesuré.
- Le comportement de la voie A/B sur un système sans pilote Vulkan matériel.

## Et le « quasi statique », avec seulement libvulkan en dynamique ?

Question posée, réponse mesurée : **non**, pour deux raisons indépendantes.

### 1. Vulkan seul ne suffit pas : Gio a besoin d'EGL

Un binaire construit avec `-tags "nox11 noopengl"` (Wayland + Vulkan, sans EGL)
se **lie** parfaitement :

```console
$ readelf -d mysql-schema-diagram-gui | grep NEEDED
  (NEEDED)  Shared library: [libc.musl-x86_64.so.1]
```

Une seule dépendance dynamique : musl. Tout le reste — wayland-client,
wayland-cursor, wayland-egl, xkbcommon, libffi — est lié à l'intérieur, et le
chargeur Vulkan est chargé à l'exécution par `dlopen`. C'est exactement le
« quasi statique » demandé.

**Mais il n'ouvre pas de fenêtre.** Une trace des appels système montre qu'il ne
se connecte même pas au socket Wayland (`connect()` absent, seulement des
`openat` sur `/proc` et `/sys`) : le pilote Wayland de Gio ne s'initialise pas.
La raison est le tag `noopengl` — le backend Linux de Gio a besoin de la glu EGL
même lorsqu'il rend par Vulkan.

### 2. Avec EGL, la surface dynamique devient Mesa, pas Vulkan

En retirant `noopengl` :

```console
$ readelf -d mysql-schema-diagram-gui | grep NEEDED
  (NEEDED)  Shared library: [libEGL.so.1]
  (NEEDED)  Shared library: [libffi.so.8]
  (NEEDED)  Shared library: [libc.musl-x86_64.so.1]
```

`libEGL.so.1` vient de **libglvnd**, et celui-ci a besoin de la version
**partagée** de `libwayland-client` (l'édition de liens le signale :
`libwayland-client.so.0, needed by libEGL.so, not found`). On ne peut donc pas
tout lier en statique et ne garder que Vulkan : EGL entre dans la danse, et avec
lui la pile graphique.

### 3. Limite transverse : un binaire musl ne charge pas de bibliothèque glibc

Dernier obstacle, mesuré : le binaire musl qui tente de charger la pile Mesa de
cette machine (compilée contre **glibc**) échoue au déplacement des symboles :

```
libGLdispatch.so.0: __memcpy_chk: symbol not found
libGLdispatch.so.0: __isoc23_strtol: symbol not found
```

Ce n'est pas un défaut du binaire : c'est la règle. **Un binaire musl est
utilisable sur une distribution musl** (Alpine, où Mesa est compilé contre musl),
pas sur une distribution glibc. Ce qui déplace la question : le « quasi statique »
n'est pas portable au sens où on l'entend d'habitude — il est portable *entre
systèmes musl*.

### Ce qu'il faudrait pour trancher le quasi statique

Exécuter le binaire **dans un conteneur Alpine** muni de Mesa et du socket
Wayland de l'hôte (`--device /dev/dri`, `WAYLAND_DISPLAY` partagé). C'est le
prochain essai à faire si cette voie est retenue ; il n'a pas été fait ici.

### Récapitulatif des trois voies

| | Liage | Exécution | Dépendances dynamiques |
| --- | --- | --- | --- |
| Pleinement statique, Vulkan seul | ✅ | ❌ (pas d'EGL) | aucune — mais musl interdit `dlopen` |
| Quasi statique, EGL inclus | ✅ | à valider sur Alpine | musl + libEGL + libffi (+ chaîne Mesa) |
| Dynamique (actuel) | ✅ | ✅ | glibc + X11/Wayland/GL/Vulkan |
