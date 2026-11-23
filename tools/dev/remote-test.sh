#!/bin/sh
# Vet and test packages of this repo on the devbox. Go builds and tests never
# run on the Mac.
#
#   tools/dev/remote-test.sh [-race] [-v] [-short] [-run REGEX] [-timeout D]
#                            [-fuzz NAME -fuzztime D] DIR...
#
# DIR is a package directory relative to the repo root, such as internal/llm.
# The script copies go.mod, go.sum, internal/core and each DIR to the devbox,
# one directory at a time, so that inside a copied directory files gone here
# are deleted there and nothing outside those directories is touched: several
# people can run it at once on different directories. It then runs gofmt -l,
# go vet and go test for ./DIR/... there. Its exit status is the remote one.
#
# With -fuzz only the named directory itself is tested, not what is below it,
# because go test fuzzes one package at a time.
#
# A package under test may import only internal/core and packages inside the
# DIRs named in the same run.
#
# To replace this file, write the new one beside it and rename it into place:
# a shell that is running the old one keeps reading the old one.
set -eu
cd "$(dirname "$0")/../.."

HOST=${GNOSH_DEV_HOST:?set GNOSH_DEV_HOST to the devbox, as user@address (and GNOSH_DEV_KEY to its ssh key, when one is needed)}
KEY=${GNOSH_DEV_KEY:-}
REMOTE=${GNOSH_DEV_DIR:-/root/src/gnosh}

flags=""
dirs=""
while [ $# -gt 0 ]; do
	case "$1" in
	-race | -v | -short)
		flags="$flags $1"
		shift
		;;
	-run | -timeout | -count | -bench | -fuzz | -fuzztime)
		flags="$flags $1 '$2'"
		shift 2
		;;
	-*)
		echo "remote-test: unknown flag $1" >&2
		exit 2
		;;
	*)
		d=${1%/}
		d=${d#./}
		[ -d "$d" ] || {
			echo "remote-test: no such directory: $d" >&2
			exit 2
		}
		dirs="$dirs $d"
		shift
		;;
	esac
done
[ -n "$dirs" ] || {
	echo "usage: tools/dev/remote-test.sh [-race] [-v] [-run REGEX] DIR..." >&2
	exit 2
}

# (Keepalives: a connection that dies on the way fails the run within two
# minutes instead of hanging it.)
SSH="ssh ${KEY:+-i $KEY -o IdentitiesOnly=yes} -o BatchMode=yes -o ConnectTimeout=20 -o ServerAliveInterval=30 -o ServerAliveCountMax=4"

mk=""
pkgs=""
for d in internal/core $dirs; do
	mk="$mk $REMOTE/$d"
done
for d in $dirs; do
	case "$flags" in *-fuzz*) pkgs="$pkgs ./$d" ;; *) pkgs="$pkgs ./$d/..." ;; esac
done

# shellcheck disable=SC2086,SC2029
$SSH "$HOST" "mkdir -p $mk"

mods="go.mod"
[ -f go.sum ] && mods="$mods go.sum"
# shellcheck disable=SC2086
rsync -az -e "$SSH" $mods "$HOST:$REMOTE/"

# One directory at a time, each onto its own place: --delete then reaches
# only inside that directory. (Copying them in one go with --relative made
# rsync treat internal/ itself as copied and delete its other children.)
# (The repository's root, given as ".", leaves out what git ignores at the
# top, dist/ above all: the Mac trial's builds are hundreds of megabytes
# and no test reads them.)
for d in internal/core $dirs; do
	if [ "$d" = . ]; then
		rsync -az --delete --exclude=/dist/ --exclude=/.git/ -e "$SSH" "$d/" "$HOST:$REMOTE/$d/"
	else
		rsync -az --delete -e "$SSH" "$d/" "$HOST:$REMOTE/$d/"
	fi
done

# shellcheck disable=SC2086,SC2029
$SSH "$HOST" "cd $REMOTE && export PATH=/usr/local/go/bin:\$PATH && bad=\$(gofmt -l $dirs) && { [ -z \"\$bad\" ] || { echo 'gofmt -w needed for:'; echo \"\$bad\"; exit 1; }; } && go vet $pkgs && go test -count=1 $flags $pkgs"
