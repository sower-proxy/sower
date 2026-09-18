#!/bin/sh
# sower one-shot installer.
#
#   curl -fsSL https://raw.githubusercontent.com/sower-proxy/sower/master/install.sh | sudo sh
#
# Downloads the release tarball for this machine, installs the sower client
# binary plus a starter config, and wires up the platform service:
#
#   * Linux with systemd   -> /etc/systemd/system/sower.service
#   * OpenWrt (procd)      -> /etc/init.d/sower
#   * macOS (launchd)      -> /Library/LaunchDaemons/sower.plist
#
# Re-running the script upgrades in place and keeps the existing config.
# Only POSIX sh features are used so busybox ash on routers works too.

set -eu

REPO="sower-proxy/sower"
TMP=""
PLATFORM=""
PREFIX=""
CONFIG_DIR=""

VERSION="latest"
MIRROR=""
SERVICE="auto"
WITH_SERVER="no"
DRY_RUN="no"
TARBALL=""
INSECURE="no"

usage() {
	cat <<'EOF'
Install or upgrade the sower client.

Usage: install.sh [options]

Options:
  -v, --version <tag>    release tag to install (default: latest)
  -m, --mirror <url>     prefix added to the download URL, e.g.
                         https://ghfast.top/ (for hosts that cannot reach GitHub)
  -p, --prefix <dir>     binary directory (default: /usr/local/bin)
  -c, --config-dir <dir> config directory (default: /etc/sower,
                         macOS: /usr/local/etc/sower)
  -s, --service <name>   service backend: auto, systemd, procd, launchd, none
  -t, --tarball <path>   install from a local release tarball (offline)
      --with-server      also install the sowerd binary (no service)
      --insecure-skip-verify
                         install even when the release publishes no
                         SHA256SUMS (remote installs verify by default)
      --dry-run          print every action without touching the system
  -h, --help             show this help

Examples:
  curl -fsSL .../install.sh | sudo sh
  sudo sh install.sh --version v1.6.0
  sudo sh install.sh --mirror https://ghfast.top/
  sudo sh install.sh --service none --prefix "$HOME/.local/bin"
EOF
}

log() { printf '%s\n' "$*"; }

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

run() {
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] $*"
		return 0
	fi
	"$@"
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		-v | --version)
			shift
			[ $# -gt 0 ] || die "--version needs a value"
			VERSION="$1"
			;;
		-m | --mirror)
			shift
			[ $# -gt 0 ] || die "--mirror needs a value"
			MIRROR="$1"
			;;
		-p | --prefix)
			shift
			[ $# -gt 0 ] || die "--prefix needs a value"
			PREFIX="$1"
			;;
		-c | --config-dir)
			shift
			[ $# -gt 0 ] || die "--config-dir needs a value"
			CONFIG_DIR="$1"
			;;
		-s | --service)
			shift
			[ $# -gt 0 ] || die "--service needs a value"
			SERVICE="$1"
			;;
		-t | --tarball)
			shift
			[ $# -gt 0 ] || die "--tarball needs a value"
			TARBALL="$1"
			;;
		--with-server) WITH_SERVER="yes" ;;
		--insecure-skip-verify) INSECURE="yes" ;;
		--dry-run) DRY_RUN="yes" ;;
		-h | --help)
			usage
			exit 0
			;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
		shift
	done
}

detect_platform() {
	os=$(uname -s)
	arch=$(uname -m)

	case "$os" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*) die "unsupported OS: $os (see README for manual install)" ;;
	esac

	case "$arch" in
	x86_64 | amd64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	armv7l | armv7 | armv6l | arm) arch="arm" ;;
	riscv64) arch="riscv64" ;;
	loongarch64 | loong64) arch="loong64" ;;
	mips) arch="mips" ;;
	mipsel | mipsle) arch="mipsle" ;;
	*) die "unsupported architecture: $arch (see README for manual install)" ;;
	esac

	PLATFORM="$os-$arch"
}

detect_service() {
	[ "$SERVICE" != "auto" ] && return 0

	if [ "$(uname -s)" = "Darwin" ]; then
		SERVICE="launchd"
	elif [ -f /etc/openwrt_release ] || command -v ubus >/dev/null 2>&1; then
		SERVICE="procd"
	elif [ -d /run/systemd/system ]; then
		SERVICE="systemd"
	else
		SERVICE="none"
	fi
}

default_paths() {
	# Keep the defaults aligned with the packaged service files.
	if [ -z "$PREFIX" ]; then
		case "$SERVICE" in
		procd) PREFIX="/usr/sbin" ;;
		*) PREFIX="/usr/local/bin" ;;
		esac
	fi
	if [ -z "$CONFIG_DIR" ]; then
		case "$SERVICE" in
		launchd) CONFIG_DIR="/usr/local/etc/sower" ;;
		*) CONFIG_DIR="/etc/sower" ;;
		esac
	fi
}

need_root() {
	[ "$DRY_RUN" = "yes" ] && return 0
	[ "$(id -u)" = "0" ] && return 0
	# A user-local install without a service needs no privileges.
	[ "$SERVICE" = "none" ] && return 0
	die "must run as root (use sudo), or pass --service none --prefix/--config-dir"
}

download() {
	# download <url> <dest>
	# Every network call is bounded: the installer may run unattended as root.
	if command -v curl >/dev/null 2>&1; then
		run curl -fL --retry 3 --connect-timeout 15 --max-time 600 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		run wget -T 30 -t 3 -O "$2" "$1"
	else
		die "need curl or wget to download the release"
	fi
}

release_url() {
	# release_url <file>
	if [ "$VERSION" = "latest" ]; then
		printf '%s' "${MIRROR}https://github.com/${REPO}/releases/latest/download/$1"
	else
		printf '%s' "${MIRROR}https://github.com/${REPO}/releases/download/${VERSION}/$1"
	fi
}

fetch_tarball() {
	if [ -n "$TARBALL" ]; then
		[ -f "$TARBALL" ] || die "no such tarball: $TARBALL"
		log "using local tarball $TARBALL"
		cp "$TARBALL" "$TMP/release.tar.gz"
		return 0
	fi

	asset="sower-${PLATFORM}.tar.gz"
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] download sower-${PLATFORM}.tar.gz ($VERSION)"
		return 0
	fi

	log "downloading $asset ($VERSION)"
	download "$(release_url "$asset")" "$TMP/release.tar.gz"
	verify_checksum "$asset"
}

skip_verify() {
	# A remote archive is installed as root, so a missing checksum is an error
	# unless the operator explicitly opts out.
	[ "$INSECURE" = "yes" ] && return 0
	die "$1 (re-run with --insecure-skip-verify to override)"
}

verify_checksum() {
	sums="$TMP/SHA256SUMS"
	if ! download "$(release_url "SHA256SUMS")" "$sums" 2>/dev/null || [ ! -f "$sums" ]; then
		log "WARNING: release $VERSION publishes no SHA256SUMS"
		skip_verify "cannot verify the downloaded archive"
		return 0
	fi

	expected=$(awk -v name="$1" '$2 == name { print $1 }' "$sums" 2>/dev/null || true)
	if [ -z "$expected" ]; then
		log "WARNING: SHA256SUMS does not list $1"
		skip_verify "cannot verify the downloaded archive"
		return 0
	fi

	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$TMP/release.tar.gz" | awk '{ print $1 }')
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$TMP/release.tar.gz" | awk '{ print $1 }')
	else
		log "WARNING: neither sha256sum nor shasum is available"
		skip_verify "cannot verify the downloaded archive"
		return 0
	fi

	[ "$actual" = "$expected" ] || die "checksum mismatch for $1 (want $expected, got $actual)"
	log "checksum verified"
}

unpack() {
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] tar -xzf release.tar.gz"
		return 0
	fi
	extract="$TMP/extract"
	mkdir -p "$extract"
	tar -xzf "$TMP/release.tar.gz" -C "$extract"
	[ -f "$extract/sower" ] || die "release tarball has no sower binary"
}

install_binaries() {
	# Replace atomically: a running process keeps the old inode, so a plain
	# copy over it fails with "text file busy".
	for name in sower sowerd; do
		if [ "$name" = "sowerd" ] && [ "$WITH_SERVER" != "yes" ]; then
			continue
		fi
		if [ "$DRY_RUN" = "yes" ]; then
			log "[dry-run] install $PREFIX/$name"
			continue
		fi
		src="$TMP/extract/$name"
		[ -f "$src" ] || die "release tarball has no $name binary"
		log "installing $PREFIX/$name"
		mkdir -p "$PREFIX"
		cp "$src" "$PREFIX/$name.new"
		chmod 0755 "$PREFIX/$name.new"
		mv "$PREFIX/$name.new" "$PREFIX/$name"
	done
}

install_config() {
	dst="$CONFIG_DIR/sower.toml"

	if [ -f "$dst" ]; then
		log "keeping existing config $dst"
		return 0
	fi
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] write starter config $dst"
		return 0
	fi

	src="$TMP/extract/sower.toml"
	[ -f "$src" ] || die "release tarball has no sower.toml"
	log "writing starter config $dst"
	mkdir -p "$CONFIG_DIR"
	# The config carries the upstream password, keep it private.
	cp "$src" "$dst"
	chmod 0600 "$dst"
}

render_service() {
	# render_service <template> <dest> : rewrite the packaged paths. The
	# placeholders keep a rewritten path from matching a later rule (e.g.
	# /usr/local/etc/sower already contains /etc/sower).
	sed -e "s|/usr/local/bin/sower|__PREFIX__/sower|g" \
		-e "s|/usr/sbin/sower|__PREFIX__/sower|g" \
		-e "s|/usr/local/etc/sower|__CONFIG__|g" \
		-e "s|/etc/sower|__CONFIG__|g" \
		-e "s|__PREFIX__|$PREFIX|g" \
		-e "s|__CONFIG__|$CONFIG_DIR|g" \
		"$1" >"$2"
}

write_root_file() {
	# write_root_file <src> <dst> <mode>
	log "writing $2"
	mkdir -p "$(dirname "$2")"
	cp "$1" "$2"
	chmod "$3" "$2"
}

install_systemd() {
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] write /etc/systemd/system/sower.service"
		log "[dry-run] systemctl daemon-reload && systemctl enable --now sower"
		return 0
	fi
	tpl="$TMP/extract/sower.service"
	[ -f "$tpl" ] || die "release tarball has no sower.service"
	render_service "$tpl" "$TMP/sower.service.gen"
	write_root_file "$TMP/sower.service.gen" "/etc/systemd/system/sower.service" 0644
	run systemctl daemon-reload
	run systemctl enable sower
	run systemctl restart sower
}

install_procd() {
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] write /etc/init.d/sower"
		log "[dry-run] /etc/init.d/sower enable && /etc/init.d/sower restart"
		return 0
	fi
	tpl="$TMP/extract/sower.init"
	[ -f "$tpl" ] || die "release tarball has no sower.init"
	render_service "$tpl" "$TMP/sower.init.gen"
	write_root_file "$TMP/sower.init.gen" "/etc/init.d/sower" 0755
	run /etc/init.d/sower enable
	run /etc/init.d/sower restart
}

install_launchd() {
	if [ "$DRY_RUN" = "yes" ]; then
		log "[dry-run] write /Library/LaunchDaemons/sower.plist"
		log "[dry-run] launchctl load -w /Library/LaunchDaemons/sower.plist"
		return 0
	fi
	tpl="$TMP/extract/sower.plist"
	[ -f "$tpl" ] || die "release tarball has no sower.plist"
	render_service "$tpl" "$TMP/sower.plist.gen"
	plist="/Library/LaunchDaemons/sower.plist"
	write_root_file "$TMP/sower.plist.gen" "$plist" 0644
	run launchctl unload -w "$plist" 2>/dev/null || true
	run launchctl load -w "$plist"
}

install_service() {
	case "$SERVICE" in
	systemd) install_systemd ;;
	procd) install_procd ;;
	launchd) install_launchd ;;
	none) log "no service requested, skipping service setup" ;;
	*) die "unknown service backend: $SERVICE" ;;
	esac
}

print_summary() {
	log ""
	log "sower installed:"
	log "  binary:  $PREFIX/sower"
	log "  config:  $CONFIG_DIR/sower.toml"
	log "  service: $SERVICE"
	log ""
	log "Next: set remote.addr and remote.password in the config, then restart:"
	case "$SERVICE" in
	systemd) log "  systemctl restart sower" ;;
	procd) log "  /etc/init.d/sower restart" ;;
	launchd) log "  launchctl kickstart -k system/sower" ;;
	*) log "  $PREFIX/sower -c $CONFIG_DIR/sower.toml" ;;
	esac
}

main() {
	parse_args "$@"
	detect_platform
	detect_service
	default_paths
	need_root

	TMP=$(mktemp -d) || die "cannot create a temporary directory"
	trap 'rm -rf "$TMP"' EXIT INT TERM

	log "platform: $PLATFORM, service: $SERVICE, prefix: $PREFIX, config: $CONFIG_DIR"

	fetch_tarball
	unpack
	install_binaries
	install_config
	install_service
	print_summary
}

main "$@"
