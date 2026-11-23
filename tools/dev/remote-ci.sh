#!/bin/sh
# Build, vet and test what is committed, from a clean copy on the devbox.
#
#   tools/dev/remote-ci.sh [-race] [-short] [-gnovm] [-tidy] [REF]
#
# REF is a commit (default HEAD). Its tree is sent with git archive, so files
# that are not committed are not part of the run: this is the check that the
# repository as recorded builds and passes, whatever is lying in the working
# tree. The copy goes to a directory named after the commit and replaces any
# earlier copy of the same commit.
#
#   -race    run the tests with the race detector
#   -short   pass -short to the tests
#   -gnovm   also run internal/gnovm's own suite, which takes minutes (twenty
#            with -race) and has a timing test that wants a quiet machine;
#            without this flag that one package is built and vetted only
#   -tidy    run go mod tidy in the copy first and print the go.mod that
#            results, without touching this tree
#
# The exit status is the remote one.
set -eu
cd "$(dirname "$0")/../.."

HOST=${GNOSH_DEV_HOST:?set GNOSH_DEV_HOST to the devbox, as user@address (and GNOSH_DEV_KEY to its ssh key, when one is needed)}
KEY=${GNOSH_DEV_KEY:-}
ROOT=${GNOSH_CI_ROOT:-/root/ci}

flags=""
gnovm=0
tidy=0
ref=HEAD
while [ $# -gt 0 ]; do
	case "$1" in
	-race | -short) flags="$flags $1" ;;
	-gnovm) gnovm=1 ;;
	-tidy) tidy=1 ;;
	-*)
		echo "remote-ci: unknown flag $1" >&2
		exit 2
		;;
	*) ref=$1 ;;
	esac
	shift
done

sha=$(git rev-parse --short=12 "$ref^{commit}")
dir="$ROOT/gnosh-$sha"
# (Keepalives: a connection that dies on the way, as a laptop's network
# drop does, fails the run within two minutes instead of hanging it.)
SSH="ssh ${KEY:+-i $KEY -o IdentitiesOnly=yes} -o BatchMode=yes -o ConnectTimeout=20 -o ServerAliveInterval=30 -o ServerAliveCountMax=4"

# Room first. The copies of other commits are kept for a look afterwards,
# the newest eight of them; and Go's build cache, which grew to 39G in five
# days on the devbox and filled its disk (2026-11-16), is cleared when
# fewer than 20G are free and the cache is what holds the room (over 4G),
# or when fewer than 4G are free whatever holds them. (A disk filled by
# other things gains nothing from a small cache cleared, and every run
# would start cold.)
prep="ls -1dt $ROOT/gnosh-* 2>/dev/null | tail -n +9 | xargs -r rm -rf; mkdir -p $ROOT; free=\$(df -Pk $ROOT | awk 'NR==2 {print \$4}'); if [ \"\$free\" -le 20971520 ]; then cache=\$(du -sk \"\$(/usr/local/go/bin/go env GOCACHE)\" 2>/dev/null | cut -f1); if [ \"\${cache:-0}\" -gt 4194304 ] || [ \"\$free\" -le 4194304 ]; then echo \"remote-ci: \$((free / 1048576))G free, the build cache \$((\${cache:-0} / 1048576))G: clearing it\"; /usr/local/go/bin/go clean -cache; fi; fi"
# shellcheck disable=SC2086,SC2029
git archive "$sha" | $SSH "$HOST" "$prep; rm -rf $dir && mkdir -p $dir && tar -x -C $dir"

run="cd $dir && export PATH=/usr/local/go/bin:\$PATH && echo \"commit $sha in $dir\""
if [ "$tidy" = 1 ]; then
	run="$run && go mod tidy && echo '--- go.mod after tidy:' && cat go.mod && echo '--- end go.mod'"
fi
run="$run && bad=\$(gofmt -l \$(git -C . ls-files 2>/dev/null || find . -name '*.go' -not -path './internal/gnovm/stdlibs/*') 2>/dev/null | grep -v '^internal/gnovm/' || true) && { [ -z \"\$bad\" ] || { echo 'gofmt -w needed for:'; echo \"\$bad\"; exit 1; }; }"
run="$run && go build ./... && go vet ./..."
if [ "$gnovm" = 1 ]; then
	run="$run && go test -count=1 -timeout 60m $flags ./..."
else
	run="$run && go test -count=1 -timeout 30m $flags \$(go list ./... | grep -v '/internal/gnovm\$')"
fi

# The run goes on by itself on the devbox, writing to a log there, and this
# side follows the log: a laptop's network drop, which ended the run with
# the connection (2026-11-19, a CI at 2 of 42 packages), now only pauses
# the following, which connects again and goes on where it was. The last
# line of the log is the run's exit status.
log="$dir/ci.log"
# shellcheck disable=SC2086,SC2029
printf '%s\n' "$run" | $SSH "$HOST" "cat > $dir/ci.sh && cd $dir && : > ci.log && (setsid sh -c 'sh ./ci.sh > ci.log 2>&1; echo \"remote-ci-exit=\$?\" >> ci.log' < /dev/null > /dev/null 2>&1 &)"
part=$(mktemp)
trap 'rm -f "$part"' EXIT
off=0
while :; do
	# shellcheck disable=SC2086,SC2029
	if $SSH "$HOST" "tail -c +$((off + 1)) $log" > "$part" 2>/dev/null; then
		n=$(wc -c < "$part")
		if [ "$n" -gt 0 ]; then
			grep -v '^remote-ci-exit=' "$part" || true
			off=$((off + n))
		fi
		if code=$(sed -n 's/^remote-ci-exit=\([0-9]*\)$/\1/p' "$part") && [ -n "$code" ]; then
			exit "$code"
		fi
		sleep 5
	else
		echo "remote-ci: lost the devbox for a moment; the run goes on there, and is followed again" >&2
		sleep 20
	fi
done
