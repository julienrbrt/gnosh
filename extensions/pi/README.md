# pi-gnosh

[gnosh](https://gnosh.ai)'s Gno machines, mem and frames in
[pi](https://www.npmjs.com/package/@earendil-works/pi-coding-agent).

Built on gnosh. The extension holds none of gnosh's logic: it starts
`gnosh bridge` for the session's folder (the protocol is gnosh's
`spec/gnosh/bridge.md`), gives the model the tools the bridge lists
(`gnovm_run`, `gnovm_new`, `gnovm_list`, `gnovm_drop`), and before each
request sends what gnosh's own view keeps of the conversation. When the
model pops a frame in mem, the work done inside it leaves what pi sends,
and the messages before the push are sent byte for byte as they were, so a
provider's cached prefix still holds.

## Use

Needs a gnosh with `gnosh bridge` on `PATH`, or named with `--gnosh <program>`
(or `PI_GNOSH_PROGRAM`).

    pi install npm:pi-gnosh        # once published
    pi -e ./src/index.ts           # from a checkout

A call gnosh asks a person to approve (`gnovm_drop` deleting a machine)
is put to them with pi's confirm dialog; without a UI it does not run.

## Not yet

- No budget is sent, so gnosh never slides old rounds out of the view;
  pi's own compaction stays in charge of length.
- Each request sends the whole conversation to the bridge.

## Develop

    npm install
    npm run check
    GNOSH_BIN=/path/to/gnosh npm test   # the pi test needs a real gnosh

## License

MIT. gnosh itself is under its own license; this package only runs it.
