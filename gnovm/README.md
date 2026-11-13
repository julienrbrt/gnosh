# gnosh-gnovm-worker

The GnoVM worker as a program of its own: the process that runs one gnosh's
Gno VMs, speaking the worker protocol (`internal/gnovm/PROTOCOL.md`) on
fds 3 and 4. It is a Go module of its own so that the program holding the Gno
interpreter is built apart from gnosh.

```sh
cd gnovm
go build -o ../gnosh-gnovm-worker ./cmd/gnosh-gnovm-worker
cd ..
go build -tags nognovm -o gnosh .   # a gnosh without the GnoVM
```

gnosh looks for `gnosh-gnovm-worker` next to its executable. A gnosh built
with `-tags nognovm` needs it; one built without the tag falls back to
running itself as the worker (`gnosh gnovm-worker`).

The module requires the gnosh module through a `replace` of `../`. A
`go.work` is not committed (the repository ignores it); `go work init . ./gnovm`
makes one for editors that want both modules in one workspace.

Licence: the GNO Network General Public License, version 6, with the
Additional Terms in `internal/gnovm/LICENSE.md`; see `internal/gnovm/DESIGN.md`
§14 for why the worker is split out.
