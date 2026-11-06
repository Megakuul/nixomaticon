{
  pkgs,
  inputs,
  ...
}: {
  home.packages = with pkgs; [
    # generally useful
    gnumake # this repo's Makefile (Fedora doesn't ship make)
    vim
    ncdu
    tldr
    dig
    file
    killall
    jq
    ffmpeg
    unzip
    ripunzip
    libarchive
    gzip
    pigz
    xz
    zstd
    lz4
    p7zip
    unrar
    atool
    gnupg
    curl
    wget
    lsof
    psmisc
    man-pages # extra man pages
    tree-sitter
    nmap
    mplayer

    # multimedia
    pamixer # pulseaudio command line mixer
    playerctl # controller for media players
    yt-dlp-light # download youtube videos
    castero # music player

    # analysis
    openssl

    # remotemanagement
    teleport_18

    # wine
    winetricks

    # documentation (asciidoc lives in project flakes, it is 2.4 GB)
    typst
    typstyle
    typst-live
  ];
}
