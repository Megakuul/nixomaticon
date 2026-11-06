# Dev tooling (kubectl, cloud CLIs, databases, IaC, ...) lives in each
# project's dev flake. Docker stays: its daemon is Fedora's moby-engine.
{pkgs, ...}: {
  home.packages = with pkgs; [
    docker
    docker-compose
  ];
}
