package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/lsp"
)

// This file is the LSP tool, Claude Code's: read-only questions to the
// session's language servers (lsp.go) about where symbols are. It is
// offered only when lspServers names a server.

const lspToolName = "LSP"

// lspOperations are the questions, by the names the model gives.
var lspOperations = []string{"goToDefinition", "findReferences", "hover", "documentSymbol", "workspaceSymbol",
	"goToImplementation", "incomingCalls", "outgoingCalls"}

// lspResultsMost is the most locations or symbols one call lists, and
// lspCallSitesShown the most places of the calls of one caller.
const (
	lspResultsMost    = 100
	lspCallSitesShown = 10
)

const lspToolDescription = `Asks the language server of a file's language (gopls for Go, say) about the code: where a symbol is defined, every place that refers to it, its type and documentation, the symbols of a file or of the whole workspace, what implements an interface, and who calls a function and what it calls. Read-only: it changes nothing. Prefer it to searching text when you look for a symbol by name or meaning.

- operation: goToDefinition, findReferences, hover, goToImplementation, incomingCalls and outgoingCalls need filePath, line and character; documentSymbol needs filePath; workspaceSymbol needs query (and filePath for which language's server, when there are several).
- line and character are 1-based, characters counted as in the file's text (as Read shows its lines): put them on the symbol's name.
- After every Write and Edit of a file a server handles, the new errors and warnings it reports come to you by themselves; there is no need to ask.`

var lspToolSchema = json.RawMessage(`{"type":"object","properties":{` +
	`"operation":{"type":"string","enum":["goToDefinition","findReferences","hover","documentSymbol","workspaceSymbol","goToImplementation","incomingCalls","outgoingCalls"],"description":"What to ask."},` +
	`"filePath":{"type":"string","description":"The file, absolute or relative to the working directory."},` +
	`"line":{"type":"integer","minimum":1,"description":"The 1-based line of the symbol."},` +
	`"character":{"type":"integer","minimum":1,"description":"The 1-based character of the symbol on its line."},` +
	`"query":{"type":"string","description":"For workspaceSymbol: the name, or part of it, to look for."}},` +
	`"required":["operation"],"additionalProperties":false}`)

// lspTool is the tool of one session.
type lspTool struct {
	s *lspServers
}

// lspInput is a call's arguments.
type lspInput struct {
	Operation string `json:"operation"`
	FilePath  string `json:"filePath"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
	Query     string `json:"query"`
}

func (t *lspTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: lspToolName, Description: lspToolDescription, InputSchema: lspToolSchema}
}

// read reads a call's arguments, with the file made absolute, and says
// what is wrong with them when something is.
func (t *lspTool) read(call core.ToolCall, env *core.ToolEnv) (lspInput, error) {
	var in lspInput
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return in, fmt.Errorf("the arguments cannot be read (%v)", err)
	}
	if !slices.Contains(lspOperations, in.Operation) {
		return in, fmt.Errorf("operation %q is not one of %s", in.Operation, strings.Join(lspOperations, ", "))
	}
	if in.FilePath = strings.TrimSpace(in.FilePath); in.FilePath != "" && !filepath.IsAbs(in.FilePath) {
		cwd := t.s.l.cwd()
		if env != nil && env.Cwd != "" {
			cwd = env.Cwd
		}
		in.FilePath = filepath.Join(cwd, in.FilePath)
	}
	if in.FilePath != "" {
		in.FilePath = filepath.Clean(in.FilePath)
	}
	switch in.Operation {
	case "workspaceSymbol":
		if strings.TrimSpace(in.Query) == "" {
			return in, errors.New("workspaceSymbol needs query: the name to look for")
		}
	case "documentSymbol":
		if in.FilePath == "" {
			return in, errors.New("documentSymbol needs filePath")
		}
	default:
		if in.FilePath == "" || in.Line < 1 || in.Character < 1 {
			return in, fmt.Errorf("%s needs filePath, and line and character from 1, on the symbol", in.Operation)
		}
	}
	return in, nil
}

// Describe says which file a call reads, for the permission rules: the
// file, or for a workspace search the project.
func (t *lspTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	in, err := t.read(call, env)
	if err != nil {
		return core.Action{Kind: core.ActionRead}, err
	}
	path := in.FilePath
	if path == "" {
		path = t.s.root()
	}
	return core.Action{Kind: core.ActionRead, Paths: []string{path}, Summary: "Ask the language server " + in.Operation + " in " + path}, nil
}

// root is the folder the servers are started in.
func (s *lspServers) root() string {
	if r := s.l.cfg.Paths.ProjectRoot; r != "" {
		return r
	}
	return s.l.cfg.Paths.Cwd
}

// Run asks the server.
func (t *lspTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	in, err := t.read(call, env)
	if err != nil {
		return core.ErrorResult(err.Error() + "."), nil
	}
	srv, lang := t.s.serverFor(in.FilePath)
	if in.FilePath == "" && len(t.s.list) > 0 {
		srv = t.s.list[0]
	}
	if srv == nil {
		return core.ErrorResult(fmt.Sprintf("No language server is set up for %s files (the lspServers key names the servers and their extensions). Search the text with Grep instead.", firstOf(filepath.Ext(in.FilePath), "these"))), nil
	}
	wait := srv.cfg.RequestTimeout
	if wait <= 0 {
		wait = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	c, err := t.s.clientOf(ctx, srv)
	var text string
	if err == nil {
		// The server is told of what changed on disk since it was last
		// told, the asked file among it, in the one slot a change takes,
		// so that the answer is of the files as they are.
		release, ok := srv.edit(ctx)
		if !ok {
			err = ctx.Err()
		} else {
			t.s.refresh(ctx, c, in.FilePath)
			if in.FilePath != "" {
				if text, ok = readFileFor(in.FilePath); !ok {
					err = fmt.Errorf("%s cannot be read, or is larger than %d MiB", in.FilePath, lspFileMost>>20)
				} else {
					_, err = c.Sync(ctx, in.FilePath, lang, text)
				}
			}
			release()
		}
	}
	if err != nil {
		return core.ErrorResult(fmt.Sprintf("The language server %s could not answer: %v.", srv.cfg.Name, err)), nil
	}
	var pos lsp.Position
	if in.Line > 0 {
		lines := strings.Split(text, "\n")
		if in.Line > len(lines) {
			return core.ErrorResult(fmt.Sprintf("%s has %d lines, and line %d was asked for.", in.FilePath, len(lines), in.Line)), nil
		}
		pos = lsp.FromRunes(in.Line, in.Character, lines[in.Line-1])
	}
	f := lspFormat{l: t.s.l, files: map[string][]string{}}
	var out string
	switch in.Operation {
	case "goToDefinition", "findReferences", "goToImplementation":
		var locs []lsp.Location
		switch in.Operation {
		case "goToDefinition":
			locs, err = c.Definition(ctx, in.FilePath, pos)
		case "findReferences":
			locs, err = c.References(ctx, in.FilePath, pos)
		default:
			locs, err = c.Implementation(ctx, in.FilePath, pos)
		}
		out = f.locations(locs, "Nothing was found at "+fmt.Sprintf("%s:%d:%d", t.s.l.shownPath(in.FilePath), in.Line, in.Character)+": put line and character on the symbol's name.")
	case "hover":
		var h string
		h, err = c.Hover(ctx, in.FilePath, pos)
		out = strings.TrimSpace(h)
		if out == "" {
			out = "The server has nothing to say of that place: put line and character on a symbol's name."
		} else if where := t.s.deniedDefinition(ctx, c, in.FilePath, pos); where != "" {
			// A hover describes what is defined elsewhere, a constant's
			// value among it: one defined in a file the model may not
			// read is not shown (the second review).
			out = "What is there is defined in " + where + ", which the Read deny rules or the read block keep from you, so the server's description of it is not shown."
		}
		out = clipText(out, 8000)
	case "documentSymbol":
		var syms []lsp.Symbol
		syms, err = c.DocumentSymbols(ctx, in.FilePath)
		out = f.symbols(syms, 0, false)
		if out == "" {
			out = "The server lists no symbols in " + t.s.l.shownPath(in.FilePath) + "."
		}
	case "workspaceSymbol":
		var syms []lsp.Symbol
		syms, err = c.WorkspaceSymbols(ctx, strings.TrimSpace(in.Query))
		out = f.symbols(syms, 0, true)
		if out == "" {
			out = fmt.Sprintf("No symbol matches %q.", in.Query)
		}
	case "incomingCalls", "outgoingCalls":
		var calls []lsp.Call
		if in.Operation == "incomingCalls" {
			calls, err = c.IncomingCalls(ctx, in.FilePath, pos)
		} else {
			calls, err = c.OutgoingCalls(ctx, in.FilePath, pos)
		}
		out = f.calls(calls, in.Operation == "incomingCalls")
	}
	if err != nil {
		return core.ErrorResult(fmt.Sprintf("The language server %s could not answer: %v.", srv.cfg.Name, err)), nil
	}
	return core.TextResult(out), nil
}

// lspFormat words answers, each place as path:line:character (1-based)
// with the text of its line: a line of a file the Read deny rules or the
// read block keep from the model is left out, whatever file the server
// names (lineFor).
type lspFormat struct {
	l     *live
	files map[string][]string
	shown int
}

// denied reports whether the file at the URI is one the Read deny rules
// or the read block keep from the model.
func (f *lspFormat) denied(uri string) bool {
	path := lsp.URIPath(uri)
	return path != "" && f.l.engine != nil && f.l.engine.ReadDenied(path)
}

// place is a location as the model is shown it.
func (f *lspFormat) place(loc lsp.Location) string {
	path := lsp.URIPath(loc.URI)
	if path == "" {
		return loc.URI
	}
	lines, ok := f.files[path]
	if !ok {
		lines = f.l.lspLines(path)
		f.files[path] = lines
	}
	lineText := ""
	if at := loc.Range.Start.Line; at >= 0 && at < len(lines) {
		lineText = lines[at]
	}
	line, char := lsp.ToRunes(loc.Range.Start, lineText)
	s := fmt.Sprintf("%s:%d:%d", f.l.shownPath(path), line, char)
	if t := strings.TrimSpace(lineText); t != "" {
		s += ": " + clipText(t, 200)
	}
	return s
}

// locations lists places, or says none was found.
func (f *lspFormat) locations(locs []lsp.Location, none string) string {
	if len(locs) == 0 {
		return none
	}
	var b strings.Builder
	for i, loc := range locs {
		if i == lspResultsMost {
			fmt.Fprintf(&b, "... and %d more.\n", len(locs)-i)
			break
		}
		b.WriteString(f.place(loc) + "\n")
	}
	return b.String()
}

// symbols lists symbols, a symbol's own indented under it: but those of a
// file the Read deny rules or the read block keep from the model, whose
// names are its text.
func (f *lspFormat) symbols(syms []lsp.Symbol, depth int, withContainer bool) string {
	var b strings.Builder
	for _, s := range syms {
		if f.denied(s.Location.URI) {
			continue
		}
		if f.shown == lspResultsMost {
			b.WriteString("... and more: narrow the query.\n")
			return b.String()
		}
		f.shown++
		name := s.Name
		if withContainer && s.Container != "" {
			name = s.Container + "." + s.Name
		}
		if s.Detail != "" {
			name += " " + s.Detail
		}
		fmt.Fprintf(&b, "%s%s %s at %s\n", strings.Repeat("  ", depth), lsp.SymbolKind(s.Kind), name, f.place(s.Location))
		b.WriteString(f.symbols(s.Children, depth+1, false))
	}
	return b.String()
}

// calls lists the calls of a call hierarchy.
func (f *lspFormat) calls(calls []lsp.Call, incoming bool) string {
	if len(calls) == 0 {
		if incoming {
			return "No calls of it were found."
		}
		return "It calls nothing the server knows of."
	}
	var b strings.Builder
	calls = slices.DeleteFunc(slices.Clone(calls), func(c lsp.Call) bool { return f.denied(c.Item.URI) })
	for i, c := range calls {
		if i == lspResultsMost {
			fmt.Fprintf(&b, "... and %d more.\n", len(calls)-i)
			break
		}
		fmt.Fprintf(&b, "%s %s at %s\n", lsp.SymbolKind(c.Item.Kind), c.Item.Name, f.place(lsp.Location{URI: c.Item.URI, Range: c.Item.SelectionRange}))
		if incoming {
			uri := c.Item.URI
			for j, r := range c.Ranges {
				if j == lspCallSitesShown {
					fmt.Fprintf(&b, "  and %d more calls there.\n", len(c.Ranges)-j)
					break
				}
				b.WriteString("  calls it at " + f.place(lsp.Location{URI: uri, Range: r}) + "\n")
			}
		}
	}
	return b.String()
}

// clipText cuts s to at most n bytes, saying so.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + " [...]"
}

// deniedDefinition is the path, as the model is shown it, of a file the
// Read deny rules or the read block keep from the model in which what is
// at pos in path is defined, or "" when there is none (or the server
// cannot say).
func (s *lspServers) deniedDefinition(ctx context.Context, c *lsp.Client, path string, pos lsp.Position) string {
	if s.l.engine == nil {
		return ""
	}
	locs, err := c.Definition(ctx, path, pos)
	if err != nil {
		return ""
	}
	for _, loc := range locs {
		if p := lsp.URIPath(loc.URI); p != "" && s.l.engine.ReadDenied(p) {
			return s.l.shownPath(p)
		}
	}
	return ""
}
