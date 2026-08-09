# Import from configuration.nix:
#   imports = [ ./muxcore-mesh-trust.nix ];
#
# Expects CA at ./muxcore-mesh-ca.crt next to this file (install-mesh-trust.sh places it).

{ ... }:

{
  security.pki.certificateFiles = [
    ./muxcore-mesh-ca.crt
  ];

  networking.hosts = {
    "10.10.0.3" = [
      "gringotts"
      "admin.gringotts"
      "media.gringotts"
      "api.gringotts"
      "auth.gringotts"
      "core.gringotts"
      "health.gringotts"
    ];
  };
}
