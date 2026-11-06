{lib, ...}:
with lib; let
  craft = app: ["ai.storyteller.${app}.desktop"]; # ArtCraft, see packages/gui.nix

  defaultApps = {
    browser = ["brave.desktop"];
    text = ["nvim-tab.desktop"]; # nvim in a new kitty tab, see term/kitty.nix
    vscodium = ["codium.desktop"];
    image = ["org.gnome.Loupe.desktop"];
    photoshop = craft "photocraft";
    raw = craft "lightcraft";
    vector = craft "vectorcraft";
    video = ["vlc.desktop"];
    audio = ["vlc.desktop"];
    directory = ["org.gnome.Nautilus.desktop"];
    document = craft "wordcraft";
    spreadsheet = craft "gridcraft";
    presentation = craft "deckcraft";
    pdf = craft "pdfcraft";
    terminal = ["kitty.desktop"];
    archive = [
      "peazip-extract.desktop"
      "peazip.desktop"
    ];
  };

  mimeMap = {
    text = [
      "application/json"
      "application/toml"
      "application/x-yaml"
      "application/xml"
      "application/x-shellscript"
      "application/javascript"
      "application/x-desktop"
      "application/x-nix"
      "text/csv"
      "text/css"
      "text/javascript"
      "text/json"
      "text/markdown"
      "text/plain"
      "text/toml"
      "text/x-c"
      "text/x-c++"
      "text/x-chdr"
      "text/x-csrc"
      "text/x-c++src"
      "text/x-go"
      "text/x-java"
      "text/x-log"
      "text/x-lua"
      "text/x-makefile"
      "text/x-markdown"
      "text/x-nix"
      "text/x-python"
      "text/x-rust"
      "text/x-shellscript"
      "text/x-typescript"
      "text/xml"
      "text/yaml"
    ];
    vscodium = [
      "x-scheme-handler/vscodium"
    ];
    image = [
      "image/avif"
      "image/bmp"
      "image/gif"
      "image/heic"
      "image/jpeg"
      "image/jpg"
      "image/jxl"
      "image/png"
      "image/svg+xml"
      "image/tiff"
      "image/vnd.microsoft.icon"
      "image/webp"
    ];
    photoshop = [
      "image/vnd.adobe.photoshop"
      "image/x-xcf"
    ];
    raw = [
      "image/x-adobe-dng"
      "image/x-canon-cr2"
      "image/x-canon-cr3"
      "image/x-fuji-raf"
      "image/x-nikon-nef"
      "image/x-olympus-orf"
      "image/x-panasonic-rw2"
      "image/x-sony-arw"
    ];
    vector = [
      "application/illustrator"
      "application/postscript"
      "image/x-eps"
    ];
    audio = [
      "audio/aac"
      "audio/flac"
      "audio/mp4"
      "audio/mpeg"
      "audio/ogg"
      "audio/opus"
      "audio/wav"
      "audio/webm"
      "audio/x-matroska"
      "audio/x-m4a"
      "audio/x-wav"
    ];
    video = [
      "video/mp2t"
      "video/mp4"
      "video/mpeg"
      "video/ogg"
      "video/quicktime"
      "video/webm"
      "video/x-flv"
      "video/x-matroska"
      "video/x-msvideo"
      "x-scheme-handler/vlc"
    ];
    browser = [
      "application/xhtml+xml"
      "text/html"
      "x-scheme-handler/about"
      "x-scheme-handler/chrome"
      "x-scheme-handler/http"
      "x-scheme-handler/https"
      "x-scheme-handler/unknown"
    ];
    document = [
      "application/msword"
      "application/rtf"
      "application/vnd.oasis.opendocument.text"
      "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
    ];
    spreadsheet = [
      "application/vnd.ms-excel"
      "application/vnd.oasis.opendocument.spreadsheet"
      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
    ];
    presentation = [
      "application/vnd.ms-powerpoint"
      "application/vnd.oasis.opendocument.presentation"
      "application/vnd.openxmlformats-officedocument.presentationml.presentation"
    ];
    pdf = [
      "application/pdf"
    ];
    directory = [
      "inode/directory"
    ];
    archive = [
      "application/bzip2"
      "application/gzip"
      "application/vnd.rar"
      "application/x-7z-compressed"
      "application/x-bzip"
      "application/x-bzip2"
      "application/x-compressed-tar"
      "application/x-gzip"
      "application/x-rar"
      "application/x-tar"
      "application/x-xz"
      "application/x-zstd-compressed-tar"
      "application/zip"
      "application/zstd"
    ];
  };

  associations = with lists;
    listToAttrs (
      flatten (mapAttrsToList (key: map (type: attrsets.nameValuePair type defaultApps."${key}")) mimeMap)
    );
in {
  xdg.configFile."mimeapps.list".force = true;
  xdg.mimeApps.enable = true;
  xdg.mimeApps.associations.added = associations;
  xdg.mimeApps.defaultApplications = associations;

  home.sessionVariables = {
    # prevent wine from creating file associations
    WINEDLLOVERRIDES = "winemenubuilder.exe=d";
  };
}
