import assert from "node:assert/strict";
import { test } from "node:test";
import { encodeFrame, FrameReader, MAX_FRAME } from "../src/wire.ts";

test("frames come back whole, however the bytes are split", () => {
  const a = encodeFrame({ id: 1, type: "result", body: { Value: "2" } });
  const b = encodeFrame({ id: 0, type: "hello", body: { magic: "gnosh-gnovm", protocol: 1 } });
  assert.equal(a.readUInt32BE(0), a.length - 4);
  const all = Buffer.concat([a, b]);
  for (let cut = 0; cut <= all.length; cut++) {
    const r = new FrameReader();
    const got = [...r.push(all.subarray(0, cut)), ...r.push(all.subarray(cut))];
    assert.deepEqual(got, [
      { id: 1, type: "result", body: { Value: "2" } },
      { id: 0, type: "hello", body: { magic: "gnosh-gnovm", protocol: 1 } },
    ]);
  }
});

test("a frame over the limit is refused", () => {
  const head = Buffer.alloc(4);
  head.writeUInt32BE(MAX_FRAME + 1, 0);
  assert.throws(() => new FrameReader().push(head), /over the/);
});
