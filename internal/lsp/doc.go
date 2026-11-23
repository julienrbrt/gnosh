// Package lsp is a client of language servers, for Claude Code's LSP tool
// and the diagnostics it reports after each edit: gnosh starts a server,
// such as gopls, speaks the Language Server Protocol with it over its
// standard input and output, tells it of the files the model reads and
// changes, keeps the diagnostics it publishes, and asks it where a symbol
// is defined, what refers to it, what type it has, what symbols a file or
// the workspace holds, what implements an interface, and who calls whom.
//
// The client is read-only as Claude Code's tool is: it never asks a
// server to change a file. What it sends of a file is what is on disk.
//
// A message is headers, an empty line and a JSON body (the protocol's base
// framing, "Content-Length: N"). A server's message may hold headers of up
// to 64 KiB and a body of up to 32 MiB, as Claude Code allows; a server
// that sends more, or text that is not a message on its output, is
// disconnected. Requests the server makes of the client are answered
// with what a client that keeps no settings has (workspace/configuration
// gets nulls, registrations and progress are accepted); anything else gets
// "method not found".
//
// Positions are the protocol's: lines and UTF-16 code units from zero.
// Position.FromRunes and ToRunes turn them into and from the 1-based lines
// and characters a model counts in a file's text.
//
// Nothing here depends on the rest of gnosh, and only the standard library
// is used.
package lsp
