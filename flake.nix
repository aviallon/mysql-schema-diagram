{
  description = "MySQL Schema Diagram — schéma physique depuis un dump SQL MySQL/MariaDB, sans serveur";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f (import nixpkgs { inherit system; }));

      # Dépendances de construction de Gio sous Linux. Le backend Linux passe par
      # cgo (X11, Wayland, Vulkan) ; sous Windows, Gio se compile en Go pur, sans
      # aucune chaîne croisée.
      gioLinuxLibs =
        pkgs: with pkgs; [
          libGL
          libxkbcommon
          vulkan-headers
          vulkan-loader
          wayland
          wayland-protocols
          wayland-scanner
          xorg.libX11
          xorg.libXcursor
          xorg.libXext
          xorg.libXfixes
          xorg.libxcb
          xorg.libXinerama
          xorg.libXi
          xorg.libXrandr
          xorg.libXrender
          xorg.libXxf86vm
        ];

      mkCLI =
        pkgs:
        (pkgs.buildGo127Module or pkgs.buildGoModule) {
          pname = "mysql-schema-diagram";
          version = self.shortRev or "dev";
          src = self;
          vendorHash = "sha256-Z7dnnM0SBEuHgAt6GdpeqbKBEDpTum3Kwx347tKYNLI=";
          subPackages = [ "cmd/mysql-schema-diagram" ];
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${self.shortRev or "dev"}"
          ];
          meta = with pkgs.lib; {
            description = "Schéma physique depuis un dump SQL MySQL/MariaDB, sans serveur";
            license = licenses.mit;
            mainProgram = "mysql-schema-diagram";
          };
        };
      mkGui =
        pkgs:
        (pkgs.buildGo127Module or pkgs.buildGoModule) {
          pname = "mysql-schema-diagram-gui";
          version = self.shortRev or "dev";
          src = self;
          vendorHash = "sha256-Z7dnnM0SBEuHgAt6GdpeqbKBEDpTum3Kwx347tKYNLI=";
          subPackages = [ "cmd/mysql-schema-diagram-gui" ];
          # Gio a besoin de cgo sous Linux (X11, Wayland, Vulkan).
          cgoEnabled = true;
          nativeBuildInputs = [ pkgs.pkg-config ];
          buildInputs = nixpkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux (gioLinuxLibs pkgs);
          ldflags = [
            "-s"
            "-w"
            "-X main.version=${self.shortRev or "dev"}"
          ];
          meta = with pkgs.lib; {
            description = "Interface graphique de MySQL Schema Diagram";
            license = licenses.mit;
            mainProgram = "mysql-schema-diagram-gui";
          };
        };
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gnumake
            pkgs.git
            pkgs.binutils # objdump : porte de staticité des .exe
            pkgs.mariadb_106 # régénération de l'oracle information_schema
          ];

          # pkg-config doit être un outil de construction, et les bibliothèques
          # des entrées de construction : c'est ce qui renseigne PKG_CONFIG_PATH,
          # sans quoi la compilation cgo de Gio ne trouve ni wayland ni xkbcommon.
          nativeBuildInputs = [ pkgs.pkg-config ];
          buildInputs = nixpkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux (gioLinuxLibs pkgs);

          shellHook = ''
            echo "MySQL Schema Diagram — dev shell (Go $(go version | cut -d' ' -f3))"
            echo "  make test    : go vet + go test"
            echo "  make build   : binaires Linux (CLI + GUI)"
            echo "  make cross   : binaires Windows, sans DLL hôte à embarquer"
            echo "  make gate    : vérifie qu'aucun .exe n'importe de DLL"
            echo "  make oracle  : régénère l'oracle information_schema depuis MariaDB"
          '';
        };
      });

      packages = forAllSystems (pkgs: {
        default = mkCLI pkgs;
        cli = mkCLI pkgs;
        gui = mkGui pkgs;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt-rfc-style);
    };
}
