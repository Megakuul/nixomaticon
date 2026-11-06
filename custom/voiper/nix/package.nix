{
  lib,
  stdenvNoCC,
  buildGoModule,
  go,
  go-licenses,
  nodejs,
  pnpm,
  fetchPnpmDeps,
  pnpmConfigHook,
  pkg-config,
  dbus,
  sipp,
  wrapGAppsHook3,
  gtk3,
  webkitgtk_4_1,
  libpulseaudio,
  alsa-lib,
  libopus,
  opusfile,
  libogg,
  speexdsp,
  libsecret,
  makeDesktopItem,
  copyDesktopItems,
  imagemagick,
}:
let
  version = "0.1.0";
  goTags = [
    "desktop"
    "production"
    "webkit2_41"
    "opus"
    "speex"
    "secretservice"
  ];
  source = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cmd
      ../internal
      ../pkg
      ../LICENSE
      (lib.fileset.difference ../web (
        lib.fileset.unions [
          (lib.fileset.maybeMissing ../web/node_modules)
          (lib.fileset.maybeMissing ../web/dist)
        ]
      ))
      ../build/package/appicon.png
      ../build/package/embed.go
    ];
  };
  frontendSource = lib.cleanSourceWith {
    src = ../web;
    filter =
      path: type:
      !(builtins.elem (baseNameOf path) [
        "node_modules"
        "dist"
        "package-lock.json"
        "package.json.md5"
      ]);
  };
  frontend = stdenvNoCC.mkDerivation {
    pname = "voiper-frontend";
    outputs = [
      "out"
      "licenses"
    ];
    inherit version;
    src = frontendSource;
    nativeBuildInputs = [
      nodejs
      pnpm
      pnpmConfigHook
    ];
    pnpmDeps = fetchPnpmDeps {
      pname = "voiper-frontend";
      src = frontendSource;
      inherit pnpm;
      fetcherVersion = 4;
      hash = "sha256-w5egfm3BdfiD2Y5cYrYFfW/+WS1cOe24nJ2llZ97lUo=";
    };
    buildPhase = ''
      runHook preBuild
      pnpm run build
      runHook postBuild
    '';
    installPhase = ''
      runHook preInstall
      cp -r dist $out
      pnpm licenses list --json > licenses-report.json
      node ${./frontend-licenses.mjs} licenses-report.json $licenses
      runHook postInstall
    '';
  };
in
buildGoModule {
  pname = "voiper";
  inherit version;
  src = source;
  vendorHash = "sha256-Fx6mcfAGJeUqVNkL8SXzRJBnKnLJsyjhlg0pVxFfcqY=";
  nativeBuildInputs = [
    go-licenses
    pkg-config
    dbus
    wrapGAppsHook3
    copyDesktopItems
    imagemagick
  ];
  buildInputs = [
    gtk3
    webkitgtk_4_1
    libpulseaudio
    alsa-lib
    libopus
    opusfile
    libogg
    speexdsp
    libsecret
  ];
  env = {
    CGO_ENABLED = "1";
    GOTOOLCHAIN = "local";
  };
  tags = goTags;
  nativeCheckInputs = [ sipp ];
  subPackages = [ "cmd/voiper" ];
  ldflags = [
    "-X github.com/megakuul/voiper/internal/version.VersionOverride=v${version}"
    "-s"
    "-w"
  ];
  preBuild = ''
    mkdir -p web/dist
    cp -r ${frontend}/. web/dist/
  '';
  checkPhase = ''
    runHook preCheck
    go test -race -timeout=3m -tags=webkit2_41,opus,speex,secretservice ./pkg/... ./internal/... ./cmd/...
    go vet -tags=webkit2_41,opus,speex,secretservice ./...
    runHook postCheck
  '';
  desktopItems = [
    (makeDesktopItem {
      name = "voiper";
      desktopName = "Voiper";
      comment = "SIP desktop phone";
      exec = "voiper %u";
      mimeTypes = [
        "x-scheme-handler/sip"
        "x-scheme-handler/sips"
        "x-scheme-handler/tel"
      ];
      icon = "voiper";
      # Matches the GTK program name, so docks group the window with this entry.
      startupWMClass = "voiper";
      keywords = [
        "phone"
        "sip"
        "voip"
        "call"
        "softphone"
        "swyx"
      ];
      categories = [
        "Network"
        "Telephony"
      ];
      terminal = false;
    })
  ];
  postInstall = ''
    install -Dm644 build/package/appicon.png $out/share/pixmaps/voiper.png
    # Launchers look icons up in the hicolor theme, at the usual sizes.
    for size in 16 24 32 48 64 128 256 512; do
      mkdir -p $out/share/icons/hicolor/''${size}x''${size}/apps
      magick build/package/appicon.png -resize ''${size}x''${size} $out/share/icons/hicolor/''${size}x''${size}/apps/voiper.png
    done
    licenses=$out/share/licenses/voiper
    install -Dm644 LICENSE $licenses/LICENSE
    # go-licenses follows the packages actually linked into the binary.
    # g722 states its license only in its README, which is shipped instead.
    export GOFLAGS="$GOFLAGS -tags=${lib.concatStringsSep "," goTags}"
    goLicenseFlags="--ignore github.com/megakuul/voiper --ignore github.com/gotranspile/g722"
    go-licenses save ./cmd/voiper --save_path=$licenses/go $goLicenseFlags
    go-licenses report ./cmd/voiper $goLicenseFlags > $licenses/go/licenses.csv
    install -Dm644 vendor/github.com/gotranspile/g722/README.md $licenses/go/github.com/gotranspile/g722/README.md
    # The Go runtime and standard library are linked statically.
    # The nixpkgs Go install omits its license, so take it from the source archive.
    mkdir -p $licenses/go/go-stdlib
    tar -xzOf ${go.src} go/LICENSE > $licenses/go/go-stdlib/LICENSE
    cp -r ${frontend.licenses} $licenses/frontend
  '';
  preFixup = ''
    gappsWrapperArgs+=(--prefix LD_LIBRARY_PATH : "${
      lib.makeLibraryPath [
        libpulseaudio
        alsa-lib
      ]
    }")
  '';
  passthru = { inherit frontend; };
  meta = {
    description = "Linux SIP desktop softphone";
    homepage = "https://github.com/megakuul/voiper";
    license = lib.licenses.mit;
    mainProgram = "voiper";
    platforms = [ "x86_64-linux" ];
  };
}
