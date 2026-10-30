# Tester les binaires Windows sous Wine

Ce document conserve la méthode et le résultat du test des `.exe` produits par
`make cross`, exécutés sous Wine dans une session KWin virtuelle. Il est écrit
parce que la partie GUI a demandé plusieurs tentatives, et parce que le résultat
n'est pas celui qu'on attend.

## Méthode

Wine a besoin d'un affichage. Une session de test Wayland pure n'en fournit pas :
on y démarre un **Xwayland privé**, et on force Wine à s'en servir (`unset
WAYLAND_DISPLAY`), car son pilote Wayland ne présente pas les fenêtres Gio.

```bash
Xwayland :90 -nolisten tcp -noreset -ac &
export DISPLAY=:90
unset WAYLAND_DISPLAY
export WINEPREFIX="$HOME/.wine-patched"   # hors /tmp : Wine refuse un préfixe
                                          # dont le parent ne lui appartient pas
nix shell nixpkgs#wine64Packages.full -c wine dist/mysql-schema-diagram-gui.exe
```

Trois pièges de cet environnement, rencontrés pour de vrai :

- **Le Wine « système » de nixpkgs (`wineWow64Packages`) ne convient pas** : pas de
  `winex11.drv` utilisable, et son `winewayland.drv` ne présente jamais le toplevel
  de Gio à KWin. Il faut `nixpkgs#wine64Packages.full` **et** X11.
- **Un préfixe Wine ne peut pas vivre dans `/tmp`** : Wine refuse de créer sa
  configuration dans un répertoire qui ne lui appartient pas.
- **La disposition clavier de Xwayland n'est pas celle de la session** : `xdotool`
  envoie des codes US, interprétés ici selon la disposition de l'hôte. Sans
  `setxkbmap -display :90 us`, les chemins tapés arrivent illisibles.

Le rendu logiciel (`LIBGL_ALWAYS_SOFTWARE`, `VK_ICD_FILENAMES=lavapipe`) avait été
imposé au départ, par précaution. **C'était inutile** — et cela a même brouillé le
diagnostic, en laissant croire à un problème de moteur de rendu.

## Résultat — le CLI fonctionne

Le binaire Windows **cross-compilé depuis Linux** s'exécute sous Wine et produit
les quatre livrables. La preuve la plus forte n'est pas la capture mais la
comparaison : en reconstruisant **les deux binaires depuis la même source**, les
sorties sont identiques.

| Fichier | Linux (Go pur) vs Windows (Wine) |
| --- | --- |
| `schema.json` | **identique** |
| `schema.svg` | **identique** |
| `relations.svg` | **identique** |
| `schema_physique.html` | identique **à l'horodatage de génération près** |

Piège rencontré : la première comparaison utilisait un `dist/mysql-schema-diagram` Linux
**périmé** et montrait de vraies différences (65 colonnes contre 66, une vue
manquante). Un binaire de comparaison doit être reconstruit, pas réutilisé.

## Résultat — la GUI fonctionne, mais Gio doit être corrigé

**La cause est identifiée, avec preuve instrumentée.** Wine ne fournit qu'un
**stub** pour `EnableMouseInPointer` (API Windows 10 ; winehq bug 53847) : l'appel
renvoie `ERROR_CALL_NOT_IMPLEMENTED`. Or Gio v0.10.3 traite cet échec comme
**fatal** dans `window.init()` — la fenêtre n'est jamais créée.

Ce qui a rendu le diagnostic long, c'est que **Gio masque sa propre erreur** :
`newWindow` envoie son signal de fin *avant* de mettre le `DestroyEvent` en file,
si bien que le client voit un pilote nul et une file vide, et lève un message
générique — « window initialization failed without a DestroyEvent » — au lieu de
la vraie cause. Rien dans les canaux de débogage de Wine ne la révèle.

Deux causes secondaires, découvertes en route :

- **Gio ne gère que `WM_POINTER*`**, sans repli sur les `WM_MOUSE*` classiques :
  même fenêtre ouverte, la souris resterait inerte sous Wine.
- **Un préfixe Wine de nixpkgs n'embarque aucune police** : la fenêtre s'ouvre,
  mais sans le moindre texte.

### La recette

```console
$ nix develop
$ make cross                  # la GUI Windows est patchée par défaut
$ unset WAYLAND_DISPLAY       # le Wine « système » de nixpkgs est wayland-only
$ nix shell nixpkgs#wine64Packages.full -c wine dist/mysql-schema-diagram-gui.exe
```

« make cross » dépend de « vendor-patched », qui vendore Gio et lui applique le
correctif **s'il ne l'est pas déjà** : c'est le chemin par défaut, parce que ces
binaires servent d'abord à être exécutés sous Wine. `make wine-compat` ne
construit que la GUI, et `make clean-vendor` revient à Gio non patché.

Le correctif (79 lignes sur `vendor/gioui.org/app/os_windows.go`) tolère
`EnableMouseInPointer` et traduit `WM_MOUSE*`/`WM_MOUSEWHEEL` en événements
pointeur. Il s'applique à un arbre **vendorié** — `vendor/` est ignoré par Git et
recréé par `make wine-compat`, jamais commité.

Côté application, la police **Go** est embarquée au lieu de compter sur les
polices du système, ce qui rend la fenêtre lisible même sur un préfixe nu.

Résultat vérifié : la fenêtre s'affiche, **le texte aussi**, les clics sont
traités, et une analyse complète est allée au bout **depuis la GUI sous Wine** —
19 tables, 66 colonnes, 32 index, 3 relations déduites, les quatre livrables
écrits sur le disque. Captures :
`~/.local/share/edm-schema/research/gio-sous-wine-artifacts/capture-mysql-schema-diagram-gui-wine-*.png`.

### Ce qu'il faut retenir des tentatives infructueuses

Elles n'étaient pas inutiles : elles ont écarté, une par une, l'affichage, le
moteur de rendu, la session et la version de Wine. Le rendu — Vulkan logiciel,
Vulkan matériel, DXVK, wined3d sur OpenGL, Proton — **n'a jamais été le
problème**, ce qui explique que tout cela n'ait rien changé.

Deux pièges méritent d'être notés pour la suite :

- Le Wine « système » de nixpkgs (`wineWow64Packages`) n'expose pas de
  `winex11.drv` utilisable et son `winewayland.drv` ne présente jamais le
  toplevel de Gio à KWin : il faut `nixpkgs#wine64Packages.full` et **X11**.
  `wine-staging` de nixpkgs est en 32 bits, donc inutilisable ici.
- Dans un bac à sable Flatpak, `DISPLAY` vaut `:0` — le Xwayland de **l'hôte**,
  pas celui d'une session virtuelle. Une fenêtre peut donc s'ouvrir sur le bureau
  réel sans qu'on s'en aperçoive.
