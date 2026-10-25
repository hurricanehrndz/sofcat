# Native libraries the Wails UI links against on Linux (cgo: GTK 4 and
# WebKitGTK 6.0). NixOS has no global /usr/lib, so the linker only finds them
# inside this shell. The toolchain itself (Go, Node, linters) stays in
# mise.toml; .envrc loads this shell first, then mise.
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      forLinux = nixpkgs.lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
      ];
    in
    {
      devShells = forLinux (system: {
        default = nixpkgs.legacyPackages.${system}.mkShell {
          packages = with nixpkgs.legacyPackages.${system}; [
            pkg-config
            gtk4
            webkitgtk_6_0
          ];
        };
      });
    };
}
