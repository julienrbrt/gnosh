# Provenance

gnosh was started from an empty repository on 13 November 2026 and written
from scratch. It is not a fork of anything. It contains no code from
Charmbracelet's Crush, and none from the earlier program of the same name that
was a fork of Crush, except the files listed in `CARRIED.md`: those are the
earlier gnosh's own work (the GnoVM machines, `mem`, the chain client, the
swarm), or gno's under gno's own licence, and each was checked against Crush's
code before it was brought over.

## How it is written

The work is split in two, and the split is the point.

**The spec side** may read the earlier tree. It writes down, in words, what
gnosh does: behaviour, formats, option names, worked examples. Its output is
`spec/`. It writes no code in this repository, and its specs hold none of
Charm's code or prompt text and no account of how Crush is put together
inside. Where a spec quotes wording a model is to be given, it is the
earlier gnosh's own (its lines about mem, its receipts and refusals), and
`CARRIED.md` lists it.

**The build side** writes all the code. It works from:

- `spec/`, `ARCHITECTURE.md`, and this repository;
- the files listed in `CARRIED.md`, once they are listed;
- public documentation of the protocols and formats it speaks (model
  providers' HTTP APIs, terminal control sequences, Unicode's data files and
  test vectors, gitignore's rules, Claude Code's user documentation);
- the Go standard library and the pinned dependencies in `go.mod`.

The build side never opens, fetches, searches for or reconstructs from memory:

- Crush's source, in the original, a fork or a mirror
  (`github.com/charmbracelet/crush`);
- the earlier gnosh / klash / crash tree (`github.com/gnosh-ai/gnosh` before
  this repository replaced it, `~/src/jaekwon/crush*`, `/root/src/gnosh-*` and
  `/root/src/klash*` on the devbox, `~/src/jaekwon/gnosh-carry` until its
  files are listed in `CARRIED.md`);
- the libraries Charm wrote for Crush's own use (`fantasy`, `catwalk`), or
  any other Charm library. Their licences would allow it; the rule is here
  so that this program's structure is its own;
- Claude Code's source code or prompt text. gnosh follows Claude Code's
  documented interface, in its own words.

A builder who recognises that it has seen another program's implementation of
the thing it is writing designs from the spec and from first principles, and
does not rebuild that implementation.

## How files are carried over

The spec side compared every file the earlier gnosh had added against Charm's
code: the tree at the point the fork began, upstream's tree today, and every
line upstream has added since. It measured the share of a file's significant
lines that also stand in Charm's code and the longest run of consecutive
lines the two share, and it took git's own copy and rename detection as a
second opinion. A file with no meaningful overlap was staged, byte for byte.
A file with overlap was not staged, and what it did is written again from
the spec.

A porter on the build side reads the staged files and nothing else of the
earlier tree. It brings a file in with its import paths changed and nothing
more, except where the file leaned on a helper of the earlier harness: such
a helper is written again from what its callers need.

Twenty files were their author's own work except for one block of four to
seven lines that also stands in Crush. They come over, if at all, only as
copies with that block cut out.

Gno's standard library and gno's B+tree package come under gno's own licence,
with its notices beside them.

`CARRIED.md` lists every file brought over with its old path, its numbers
from the scan, the checksum of the staged original, and what was changed.

The session that directs the work wrote `internal/core` and the build
briefs. It read the earlier tree's file names and sizes and its licence, and
did not open its Crush-derived source.

## What is kept as evidence

- `spec/`: what the build side was given.
- `CARRIED.md`: every file brought over, where it came from, and the numbers
  from the scan that compared it with Crush's code.
- `AUTHORSHIP.md`: who asked for what, in their own words.
- The git history, which starts empty.
- `tools/audit/carried.py check`, which re-derives every carried file's
  staged original from the tree and compares checksums.
- `tools/audit/similarity/`: a comparison of this tree against every
  published version of Crush and against the Charm libraries the earlier
  gnosh used, by shared lines, shared runs of lines, shared runs of tokens,
  and shared runs of tokens with every name set aside, calibrated on code
  that is known to be Crush's and on unrelated Go programs. Its README gives
  the findings, most serious first, and what such a comparison cannot show.
  It is to be run again on whatever commit is published.

## Dependencies

The harness itself (everything that is not the carried GnoVM subsystem)
uses the standard library and two of the Go team's own modules. The carried
GnoVM subsystem needs gno and a pure-Go SQLite, and those bring what they
need. Every version is the one the carried code was built and tested with
in the earlier gnosh, and none is newer than five weeks at the time of
writing.

| module | version | released | used by | why it is settled enough |
|---|---|---|---|---|
| `golang.org/x/term` | v0.47.0 | 2026-10-09 | the terminal, the chain client's password prompt | The Go team's own; raw mode and terminal size only. |
| `golang.org/x/sys` | v0.49.0 | 2026-10-06 | the terminal, the shell tool, file locks | The Go team's own. |
| `github.com/gnolang/gno` | v1.5.1-0.20260928125126-5cdbc25fcde0 | 2026-09-28 | `internal/gnovm`, `internal/gnochain` | Gno itself: the VM that `mem` runs in. Pinned to one commit; the embedded standard library is that commit's. |
| `modernc.org/sqlite` | v1.58.0 | 2026-09-01 | `internal/gnovm` (mem's journal and index) | SQLite translated to Go, no C. The files already on Jae's disks are in its format. |
| `golang.org/x/mod` | v0.42.0 | 2026-10-09 | `internal/gnovm` | The Go team's own; version comparison. |
| `github.com/stretchr/testify` | v1.12.1 | 2026-08-17 | tests of the carried code only | Not in the program. |

What those need in turn (cryptography, protobuf, LevelDB and the rest) is
listed as indirect in `go.mod`, each at the version the earlier gnosh
selected.

## Data that is not gnosh's

| what | where | whose | terms |
|---|---|---|---|
| Unicode Character Database 17.0.0: the tables generated from it, and two of its test files | `internal/text/tables.go`, `internal/text/testdata/` | Unicode, Inc. | Unicode License V3 (`internal/text/LICENSE-UNICODE.txt`) |
| The numbered examples of the CommonMark Spec 0.31.2 and the GitHub Flavored Markdown Spec 0.29, used by tests only | `internal/markdown/testdata/spec/` | John MacFarlane; GitHub | CC BY-SA 4.0 (`internal/markdown/testdata/spec/NOTICE.md`) |
| The HTML 4 named character references (names and code points), from Python's `html.entities` table | `internal/markdown/entity.go` | facts from the HTML standard | none needed; the generator is beside it |
| The BIP-0039 English word list, 2048 words (SHA-256 2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda, the one BIP-0039 gives), as trezor/python-mnemonic carries it, unchanged: what mem's secret filter knows a wallet's recovery phrase by | `internal/gnovm/bip39english.txt` | Pavol Rusnak | MIT (`internal/gnovm/LICENSE-BIP39.txt`) |
| Two of the Gno project's guides, `gno-interrealm-v2.md` and `gno-security-guide.md`, from gnolang/gno's `docs/resources` at bc88c1aec59eebe2da6ce8027dc5e96c2ebcf273, unchanged: what the model of a Gno project audits new Gno code against | `internal/gnodocs/` | NewTendermint, LLC (gnolang/gno) | the GNO Network General Public License, version 6, as gnosh's own |
