# pi-gnovm

A [pi](https://pi.dev) extension that gives a pi session gnosh's Gno VMs and
mem, and cuts what a mem pop removes from the model's context, as gnosh does.

It links no interpreter. It starts gnosh's GnoVM worker,
`gnosh-gnovm-worker`, and speaks the worker protocol to it
(`internal/gnovm/PROTOCOL.md`): length-prefixed JSON frames on the worker's
fds 3 and 4. The VMs, mem and their journals are the worker's; this package
is about 900 lines of TypeScript around them.

## What it does

- `gnovm_run`: runs code in a VM (your scratch VM, a named one, or `mem`)
  and shows the result as gnosh's tool does.
- `gnovm_list`: the project's VMs, the host's memory, what a VM can import.
- Frames: mem's pushes and pops come back with each `gnovm_run` result
  (`RunResult.Frames`), which keeps them in its tool result's `details`. The
  session's stack is read back from its branch whenever it is needed, so it
  holds across a restart, a `/tree` move or a fork. A mem call is sent the
  stack (`RunRequest.Frames`).
- The cut: on pi's `context` event, before each request, everything a pop
  removed is left out of that request's copy of the messages: what came
  after the push's round and before the pop's call, but for the user's
  words in the pop's own turn. The push's answer gets a short note of what
  came after it. Nothing earlier is edited and the session file is not
  touched, so each request after a pop sends the same list, and a
  provider's prompt cache of everything through the push still holds. This
  ports `cutPopped` and the parts it needs from `internal/agent/frames.go`.

A pi session and a gnosh session on the same project share its mem: the
worker's data directory is gnosh's (`$GNOSH_GLOBAL_DATA/gnovm`, else
`$XDG_DATA_HOME/gnosh/gnovm`, else `~/.local/share/gnosh/gnovm`).

Not here: the close check, `/mem`, `/goal`, slides, the frames sidebar, and
mem's naming of unnamed thoughts. Those are gnosh's harness, not the
worker's.

## Use

Build the worker in gnosh (`task build:worker`), and put it on `PATH` or
name it with `--gnovm-worker`:

```sh
pi -e ./extensions/pi-gnovm --gnovm-worker ./gnosh-gnovm-worker
```

or install the directory as a pi package (`pi install ./extensions/pi-gnovm`).
pi loads the TypeScript as it is; there is no build step.

The worker refuses a client whose build is not its own when either was
built with a pinned build ID (a release). This client is not a gnosh build,
so for now it works with workers built without one (`go build`,
`task build:worker`), not with a released worker.

Memory-only VMs (your scratch VM) end with the pi process: the extension
deletes them when it closes. It takes no client lock, which gnosh's client
holds with flock, so a gnosh started on the same project meanwhile may
delete them sooner. Persistent VMs and mem are not affected.

## Develop

```sh
npm install
npm run check     # tsc
npm test          # node --test; the test against a real worker runs
                  # when GNOVM_WORKER names one
GNOVM_WORKER=../../gnosh-gnovm-worker npm test
```
