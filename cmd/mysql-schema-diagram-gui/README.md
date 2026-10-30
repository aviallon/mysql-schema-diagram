# `mysql-schema-diagram-gui` — pourquoi `go test ./...` échoue ici

Le backend Linux de Gio (X11, Wayland, Vulkan) passe par cgo. Avec
`CGO_ENABLED=0`, la compilation de ce paquet s'arrête sur un message peu parlant :

```
imports gioui.org/internal/vk: build constraints exclude all Go files
```

Ce n'est pas une erreur de configuration du dépôt, et ce n'est pas propre à ce
paquet : c'est la conséquence de deux faits qui ne se contredisent pas.

- **Sous Windows**, Gio se compile en **Go pur** : c'est ce qui permet à la CI —
  et à `make cross` — de produire un `.exe` unique sans chaîne mingw.
- **Sous Linux**, il exige cgo.

Utilisez les cibles prévues, qui traitent les deux cas :

```console
$ nix develop
$ make test      # go vet sur tout le module en GOOS=windows, puis go test
                 # sur les paquets vérifiables sans cgo
$ make build     # binaires Linux : le CLI en Go pur, la GUI en cgo
$ make cross     # binaires Windows, Go pur
```

`make test` vérifie donc bien ce paquet — par la passe `GOOS=windows go vet`,
qui couvre l'intégralité du module — sans exiger X11, Wayland ni Vulkan sur la
machine qui teste.
