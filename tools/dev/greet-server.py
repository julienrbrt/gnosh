#!/usr/bin/env python3
"""A small MCP server for gnosh's box checks (tools/dev/box-ui-check.sh,
the agentmcp scenario): one tool, greet, which greets whoever it is told
to, with a word of its own that the model cannot guess, so that its answer
shows the tool really ran. It speaks newline-delimited JSON-RPC on standard
input and output, and writes each call to the log file named by its first
argument.

    python3 greet-server.py LOG
"""
import json
import sys

log = open(sys.argv[1], "a") if len(sys.argv) > 1 else None


def send(message):
    sys.stdout.write(json.dumps(message) + "\n")
    sys.stdout.flush()


for line in sys.stdin:
    try:
        m = json.loads(line)
    except ValueError:
        continue
    method, mid = m.get("method"), m.get("id")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": mid, "result": {
            "protocolVersion": "2025-06-18",
            "serverInfo": {"name": "greet", "version": "1"},
            "capabilities": {"tools": {}}}})
    elif method == "tools/list":
        send({"jsonrpc": "2.0", "id": mid, "result": {"tools": [{
            "name": "greet",
            "description": "Greets a person by name.",
            "inputSchema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}})
    elif method == "tools/call":
        who = ((m.get("params") or {}).get("arguments") or {}).get("name", "nobody")
        if log:
            log.write("greet %s\n" % who)
            log.flush()
        send({"jsonrpc": "2.0", "id": mid, "result": {"content": [
            {"type": "text", "text": "hello, %s, from the marmalade server" % who}]}})
    elif method == "ping" and mid is not None:
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
    elif method and mid is not None:
        send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "unknown method"}})
