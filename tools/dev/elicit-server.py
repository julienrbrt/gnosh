#!/usr/bin/env python3
"""A small MCP server for gnosh's box checks (tools/dev/box-ui-check.sh,
the elicit scenario): one tool, deploy, which asks the person where to
deploy (elicitation/create) in the middle of its call, and answers with
what it was told. It speaks newline-delimited JSON-RPC on standard input
and output, and writes what the client declared and answered to the log
file named by its first argument.

    python3 elicit-server.py LOG
"""
import json
import sys

log = open(sys.argv[1], "a") if len(sys.argv) > 1 else None


def note(text):
    if log:
        log.write(text + "\n")
        log.flush()


def send(message):
    sys.stdout.write(json.dumps(message) + "\n")
    sys.stdout.flush()


def read():
    line = sys.stdin.readline()
    if not line:
        sys.exit(0)
    return json.loads(line)


FORM = {
    "message": "Where should it go?",
    "requestedSchema": {
        "type": "object",
        "properties": {
            "region": {"type": "string", "title": "Region", "enum": ["us-east", "eu-west"],
                       "enumNames": ["US East", "EU West"]},
            "sure": {"type": "boolean", "title": "Sure"},
        },
        "required": ["region", "sure"],
    },
}

asked = 0
while True:
    m = read()
    method, mid = m.get("method"), m.get("id")
    if method == "initialize":
        caps = (m.get("params") or {}).get("capabilities") or {}
        note("client declares elicitation: %s" % ("elicitation" in caps))
        send({"jsonrpc": "2.0", "id": mid, "result": {
            "protocolVersion": "2025-06-18",
            "serverInfo": {"name": "elicit", "version": "1"},
            "capabilities": {"tools": {}}}})
    elif method == "tools/list":
        send({"jsonrpc": "2.0", "id": mid, "result": {"tools": [{
            "name": "deploy",
            "description": "Deploys the service, after asking the person where.",
            "inputSchema": {"type": "object", "properties": {}}}]}})
    elif method == "tools/call":
        asked += 1
        qid = "ask-%d" % asked
        send({"jsonrpc": "2.0", "id": qid, "method": "elicitation/create", "params": FORM})
        while True:
            r = read()
            if r.get("id") == qid and "method" not in r:
                break
            if r.get("method") and r.get("id") is not None:
                # (A request while it waits, such as a ping.)
                send({"jsonrpc": "2.0", "id": r["id"], "result": {}})
        res = r.get("result") or {}
        note("answer: %s" % json.dumps(r.get("result") or r.get("error"), sort_keys=True))
        if res.get("action") == "accept":
            text = "deployed to %s" % (res.get("content") or {}).get("region")
        else:
            text = "not deployed: the person's answer was %s" % res.get("action", "an error")
        send({"jsonrpc": "2.0", "id": mid, "result": {"content": [{"type": "text", "text": text}]}})
    elif method == "ping" and mid is not None:
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
    elif method and mid is not None:
        send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "unknown method"}})
