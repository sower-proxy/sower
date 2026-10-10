#!/bin/sh
# End-to-end test for install.sh.
#
# Everything runs offline against tarballs built here, with --service none into
# a throwaway prefix and config dir, so the test never needs root, never touches
# the system and never opens a network connection. It covers what `sh -n`
# cannot: the install and upgrade paths, the config-preservation contract, the
# dry-run contract, and the failure modes for a malformed release tarball.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL="$HERE/install.sh"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

fail() {
	printf 'install_e2e_test: %s\n' "$*" >&2
	exit 1
}

# stage_tarball <archive> <version> <member>...
# Builds a minimal release tarball. The binaries are shell stubs that echo
# their own name and version, so the test can tell a replaced binary from a
# stale one.
stage_tarball() {
	archive=$1
	version=$2
	shift 2
	stage="$WORK/stage-$version"
	rm -rf "$stage"
	mkdir -p "$stage"
	for member in "$@"; do
		case "$member" in
		sower | sowerd)
			printf '#!/bin/sh\necho "%s %s"\n' "$member" "$version" >"$stage/$member"
			chmod 0755 "$stage/$member"
			;;
		sower.toml)
			printf 'config %s\n' "$version" >"$stage/sower.toml"
			;;
		*)
			printf '%s\n' "$member" >"$stage/$member"
			;;
		esac
	done
	tar -czf "$archive" -C "$stage" "$@"
	rm -rf "$stage"
}

# run_install <archive> <prefix> <config-dir> [extra args...]
run_install() {
	archive=$1
	prefix=$2
	config_dir=$3
	shift 3
	sh "$INSTALL" --tarball "$archive" --service none \
		--prefix "$prefix" --config-dir "$config_dir" "$@"
}

# --- fresh install -----------------------------------------------------------

PREFIX="$WORK/prefix"
CONFDIR="$WORK/config"
stage_tarball "$WORK/release-v1.tar.gz" v1 sower sowerd sower.toml

if ! run_install "$WORK/release-v1.tar.gz" "$PREFIX" "$CONFDIR" >"$WORK/fresh.log" 2>&1; then
	fail "fresh install failed:
$(cat "$WORK/fresh.log")"
fi

[ -x "$PREFIX/sower" ] || fail "sower was not installed as an executable"
[ "$("$PREFIX/sower")" = "sower v1" ] || fail "installed sower is not the tarball's binary"
[ -f "$CONFDIR/sower.toml" ] || fail "starter config was not written"
[ "$(cat "$CONFDIR/sower.toml")" = "config v1" ] || fail "starter config content mismatch"

config_mode=$(ls -l "$CONFDIR/sower.toml" | cut -c1-10)
[ "$config_mode" = "-rw-------" ] || fail "starter config mode is $config_mode, want -rw-------"

# --- upgrade in place --------------------------------------------------------

printf 'edited by the operator\n' >"$CONFDIR/sower.toml"
stage_tarball "$WORK/release-v2.tar.gz" v2 sower sowerd sower.toml

if ! run_install "$WORK/release-v2.tar.gz" "$PREFIX" "$CONFDIR" >"$WORK/upgrade.log" 2>&1; then
	fail "upgrade failed:
$(cat "$WORK/upgrade.log")"
fi

[ "$("$PREFIX/sower")" = "sower v2" ] || fail "upgrade did not replace the binary"
[ "$(cat "$CONFDIR/sower.toml")" = "edited by the operator" ] ||
	fail "upgrade overwrote the existing config"
grep -q 'keeping existing config' "$WORK/upgrade.log" || fail "upgrade did not report keeping the config"

# --- --with-server installs sowerd -------------------------------------------

if ! run_install "$WORK/release-v2.tar.gz" "$PREFIX" "$CONFDIR" --with-server >"$WORK/server.log" 2>&1; then
	fail "--with-server install failed:
$(cat "$WORK/server.log")"
fi
[ -x "$PREFIX/sowerd" ] || fail "--with-server did not install sowerd"

# --- dry run must not touch the system --------------------------------------

DRY_PREFIX="$WORK/dry/prefix"
DRY_CONFDIR="$WORK/dry/config"
if ! run_install "$WORK/release-v1.tar.gz" "$DRY_PREFIX" "$DRY_CONFDIR" --dry-run >"$WORK/dry.log" 2>&1; then
	fail "dry run failed:
$(cat "$WORK/dry.log")"
fi
[ ! -e "$DRY_PREFIX" ] || fail "dry run created the prefix directory"
[ ! -e "$DRY_CONFDIR" ] || fail "dry run created the config directory"
grep -q '^\[dry-run\]' "$WORK/dry.log" || fail "dry run printed no action preview"

# --- malformed tarballs ------------------------------------------------------

stage_tarball "$WORK/no-config.tar.gz" v1 sower sowerd
if run_install "$WORK/no-config.tar.gz" "$WORK/bad-a/prefix" "$WORK/bad-a/config" >"$WORK/no-config.log" 2>&1; then
	fail "installer accepted a tarball without sower.toml"
fi
grep -q 'no sower.toml' "$WORK/no-config.log" ||
	fail "unexpected failure for a tarball without sower.toml:
$(cat "$WORK/no-config.log")"
[ ! -e "$WORK/bad-a/config/sower.toml" ] || fail "half-written config left behind"

stage_tarball "$WORK/no-binary.tar.gz" v1 sower.toml
if run_install "$WORK/no-binary.tar.gz" "$WORK/bad-b/prefix" "$WORK/bad-b/config" >"$WORK/no-binary.log" 2>&1; then
	fail "installer accepted a tarball without the sower binary"
fi
grep -q 'no sower binary' "$WORK/no-binary.log" ||
	fail "unexpected failure for a tarball without the sower binary:
$(cat "$WORK/no-binary.log")"
[ ! -e "$WORK/bad-b/prefix/sower" ] || fail "installed a binary from a tarball without one"

stage_tarball "$WORK/no-server.tar.gz" v1 sower sower.toml
if run_install "$WORK/no-server.tar.gz" "$WORK/bad-c/prefix" "$WORK/bad-c/config" --with-server >"$WORK/no-server.log" 2>&1; then
	fail "installer accepted --with-server from a tarball without sowerd"
fi
grep -q 'no sowerd binary' "$WORK/no-server.log" ||
	fail "unexpected failure for a tarball without sowerd:
$(cat "$WORK/no-server.log")"

# --- CLI surface -------------------------------------------------------------

if ! sh "$INSTALL" --help >"$WORK/help.log" 2>&1; then
	fail "--help exited non-zero"
fi
grep -q 'Install or upgrade the sower client' "$WORK/help.log" || fail "--help printed no usage"

if sh "$INSTALL" --not-a-flag >"$WORK/unknown.log" 2>&1; then
	fail "installer accepted an unknown option"
fi
grep -q 'unknown option' "$WORK/unknown.log" || fail "unknown option reported nothing useful"

printf 'install_e2e_test: PASS\n'
