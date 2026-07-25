#!/bin/sh
# Build this Athene application inside a container, so the GTK4 development
# libraries never have to be installed on this machine.
#
# Athene wrote this file once, when the project was created, and will never
# overwrite it — same as the Makefile and README.md, and unlike app.gen.go. Edit
# it freely.
#
# The ./app it produces is an ordinary native binary meant to be run on the host,
# not inside the container — see "Build in a container" in README.md.

set -eu

PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BIN=app
IMAGE=${ATHENE_CONTAINER_IMAGE:-athene-build:go1.24-trixie}

# The cache is deliberately outside the project: the first build compiles the
# gotk4 cgo bindings, which takes minutes, and sharing one cache across every
# Athene project means that cost is paid once per machine rather than once per
# project.
CACHE_ROOT=${ATHENE_BUILD_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/athene-build}

die() {
	echo "build-in-container.sh: $*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Build this Athene application inside a container.

  ./build-in-container.sh              compile ./app
  ./build-in-container.sh -v -x        same, passing extra flags to 'go build'
  ./build-in-container.sh --shell      open a shell in the build image
  ./build-in-container.sh --clean      delete the shared build cache
  ./build-in-container.sh --help

Environment overrides:
  ATHENE_CONTAINER_RUNTIME   podman | docker   (default: whichever is found)
  ATHENE_CONTAINER_IMAGE     image tag to build and use
  ATHENE_BUILD_CACHE         host directory holding the Go caches
EOF
}

MODE=build
case ${1:-} in
-h | --help)
	usage
	exit 0
	;;
--shell)
	MODE=shell
	shift
	;;
--clean)
	MODE=clean
	shift
	;;
esac

if [ "$MODE" = clean ]; then
	echo "==> removing $CACHE_ROOT"
	rm -rf "$CACHE_ROOT"
	exit 0
fi

# ---------------------------------------------------------------- runtime setup

RUNTIME=${ATHENE_CONTAINER_RUNTIME:-}
if [ -z "$RUNTIME" ]; then
	for candidate in podman docker; do
		if command -v "$candidate" >/dev/null 2>&1; then
			RUNTIME=$candidate
			break
		fi
	done
fi
[ -n "$RUNTIME" ] || die "no container runtime found.
Install podman or docker, or build natively with 'make build' — which needs the
GTK4 development packages listed under Prerequisites in README.md."
command -v "$RUNTIME" >/dev/null 2>&1 || die "container runtime '$RUNTIME' not found."

# A rootless runtime maps the container's root to the invoking user, so anything
# the build writes already comes out owned by you. A rootful one does not, and
# would leave a root-owned ./app and root-owned caches you cannot clean up
# without sudo — so there, pin the uid explicitly.
rootless=no
case $RUNTIME in
podman)
	[ "$("$RUNTIME" info -f '{{.Host.Security.Rootless}}' 2>/dev/null)" = true ] && rootless=yes
	;;
docker)
	"$RUNTIME" info -f '{{.SecurityOptions}}' 2>/dev/null | grep -q rootless && rootless=yes
	;;
esac
USER_FLAGS=
if [ "$rootless" = no ]; then
	# HOME too: with an unmapped uid the image's /root is not writable, and some
	# Go subcommands still want somewhere to put dotfiles.
	USER_FLAGS="--user $(id -u):$(id -g) --env HOME=/tmp"
fi

# On an SELinux host a bind mount is unreadable inside the container unless it is
# relabelled. Both runtimes accept ':z' and ignore it where SELinux is not
# enforcing, but only add it when needed — relabelling touches host labels.
MOUNT_OPT=
if command -v selinuxenabled >/dev/null 2>&1 && selinuxenabled 2>/dev/null; then
	MOUNT_OPT=:z
fi

mkdir -p "$CACHE_ROOT/go-build" "$CACHE_ROOT/go-mod"

TTY_FLAGS=
if [ "$MODE" = shell ] && [ -t 0 ]; then
	TTY_FLAGS="--interactive --tty"
fi

# run_in_image runs its arguments as a command in the build image, with the
# project bind-mounted at /src and the Go caches wired to the host cache. Taking
# the command as "$@" is what keeps the caller's own arguments correctly quoted.
run_in_image() {
	# shellcheck disable=SC2086 # deliberate word splitting of our own flag lists
	"$RUNTIME" run --rm $USER_FLAGS $TTY_FLAGS \
		--volume "$PROJECT_DIR:/src$MOUNT_OPT" \
		--volume "$CACHE_ROOT/go-build:/cache/go-build$MOUNT_OPT" \
		--volume "$CACHE_ROOT/go-mod:/cache/go-mod$MOUNT_OPT" \
		--workdir /src \
		"$IMAGE" "$@"
}

# ---------------------------------------------------------------- image + build

# Feeding the Containerfile in on stdin gives the build an empty context: nothing
# from the project is uploaded or baked into the image, and no .dockerignore is
# needed to arrange that. Layers are cached, so a repeat build of an unchanged
# image is near-instant.
echo "==> build image $IMAGE ($RUNTIME)"
"$RUNTIME" build --quiet --tag "$IMAGE" - <"$PROJECT_DIR/Containerfile" >/dev/null

if [ "$MODE" = shell ]; then
	echo "==> shell in $IMAGE (the project is at /src)"
	run_in_image bash
	exit $?
fi

# Remove any previous binary first, so a failed build cannot be mistaken for a
# successful one by whatever looks at ./app next.
rm -f "$PROJECT_DIR/$BIN"

echo "==> compile ./$BIN"
# 'go build' wants its flags before the package, so the caller's extra arguments
# land between -o and the trailing dot. No pipe here: the run's exit status has
# to reach 'set -e' unfiltered.
run_in_image sh -euc '
	go mod tidy
	exec go build -o "$0" "$@" .
' "$BIN" "$@"

[ -f "$PROJECT_DIR/$BIN" ] || die "the build produced no $BIN — see the output above."
echo "==> done: $PROJECT_DIR/$BIN"

# The binary links dynamically against the system GTK4, so it needs the GTK4
# *runtime* wherever it runs — not the -dev packages, but not nothing either.
# Check now rather than leaving the user with a bare loader error.
if command -v ldd >/dev/null 2>&1; then
	missing=$(ldd "$PROJECT_DIR/$BIN" 2>/dev/null | grep 'not found' || true)
	if [ -n "$missing" ]; then
		echo
		echo "warning: this machine is missing libraries ./$BIN needs:" >&2
		echo "$missing" >&2
		echo "Install the GTK4 runtime — Debian/Ubuntu: libgtk-4-1, Fedora: gtk4." >&2
	fi
fi
