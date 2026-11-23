#!/usr/bin/env python3
# channel-webhook.py PORT [REPLIES [QUESTIONS]]: a channel for trying
# gnosh --channels.
#
# An MCP server on stdio that declares itself a channel (the experimental
# capability claude/channel) and listens for HTTP POSTs on 127.0.0.1:PORT:
# each body is pushed into the session as an event
# (notifications/claude/channel), with a chat_id. Its one tool, reply,
# appends what the model answers to the file REPLIES, when given.
#
# Given QUESTIONS too, it relays permission questions
# (claude/channel/permission): each question gnosh sends is appended to
# that file as a line "<id> <tool>: <description> <input>", and a POST of
# "yes <id>" or "no <id>" answers it instead of being pushed as an event.
#
#   {"mcpServers": {"hook": {"command": "python3", "args": ["channel-webhook.py", "8788"]}}}
#   gnosh --channels server:hook
#   curl -d "build failed on main" 127.0.0.1:8788
#
# Python's standard library only; anyone who can reach the port can put
# words in front of the model, and answer its questions when QUESTIONS is
# given, so it listens on 127.0.0.1 alone.
import json
import re
import sys
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

lock = threading.Lock()


def send(msg):
    with lock:
        sys.stdout.write(json.dumps(msg) + "\n")
        sys.stdout.flush()


port = int(sys.argv[1])
replies = sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else None
questions = sys.argv[3] if len(sys.argv) > 3 else None
chats = [0]
VERDICT = re.compile(r"^\s*(y|yes|n|no)\s+([a-km-z]{5})\s*$", re.I)


class Hook(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0))).decode("utf-8", "replace")
        m = VERDICT.match(body) if questions else None
        if m:
            send({"jsonrpc": "2.0", "method": "notifications/claude/channel/permission",
                  "params": {"request_id": m.group(2).lower(),
                             "behavior": "allow" if m.group(1).lower().startswith("y") else "deny"}})
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"verdict sent\n")
            return
        with lock:
            chats[0] += 1
            chat = str(chats[0])
        send({"jsonrpc": "2.0", "method": "notifications/claude/channel",
              "params": {"content": body, "meta": {"chat_id": chat}}})
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok\n")


threading.Thread(target=HTTPServer(("127.0.0.1", port), Hook).serve_forever, daemon=True).start()

INSTRUCTIONS = ('Events arrive as <channel source="hook" chat_id="...">. '
                "Answer one with the reply tool, passing the chat_id from its tag.")
REPLY = {"name": "reply", "description": "Sends an answer back over the webhook channel.",
         "inputSchema": {"type": "object", "properties": {"chat_id": {"type": "string"}, "text": {"type": "string"}},
                         "required": ["chat_id", "text"]}}

for line in sys.stdin:
    try:
        m = json.loads(line)
    except ValueError:
        continue
    method, mid = m.get("method"), m.get("id")
    if method == "initialize":
        experimental = {"claude/channel": {}}
        if questions:
            experimental["claude/channel/permission"] = {}
        send({"jsonrpc": "2.0", "id": mid, "result": {
            "protocolVersion": "2025-06-18", "serverInfo": {"name": "webhook", "version": "0.1"},
            "capabilities": {"tools": {}, "experimental": experimental},
            "instructions": INSTRUCTIONS}})
    elif method == "notifications/claude/channel/permission_request" and questions:
        p = m.get("params") or {}
        with open(questions, "a") as f:
            f.write("%s %s: %s %s\n" % (p.get("request_id", "?"), p.get("tool_name", "?"),
                                         p.get("description", ""), p.get("input_preview", "")))
    elif method == "tools/list":
        send({"jsonrpc": "2.0", "id": mid, "result": {"tools": [REPLY]}})
    elif method == "tools/call":
        args = (m.get("params") or {}).get("arguments") or {}
        if replies:
            with open(replies, "a") as f:
                f.write("%s: %s\n" % (args.get("chat_id", "?"), args.get("text", "")))
        send({"jsonrpc": "2.0", "id": mid, "result": {"content": [{"type": "text", "text": "sent"}]}})
    elif method == "ping":
        send({"jsonrpc": "2.0", "id": mid, "result": {}})
    elif mid is not None:
        send({"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": "not supported"}})
