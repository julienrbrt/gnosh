package config

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"
)

// LSPServer is one entry of the lspServers key: a language server and the
// files it is for, with the fields of Claude Code's (its plugins' .lsp.json
// and their manifests' lspServers key). A server is a program gnosh runs,
// so a project's entry waits for the folder to be trusted.
type LSPServer struct {
	// Name is the entry's key ("gopls").
	Name string
	// Command, Args and Env start it.
	Command string
	Args    []string
	Env     map[string]string
	// Extensions maps a file ending (".go") to the language the server is
	// told the file is in ("go"): Claude Code's extensionToLanguage.
	Extensions map[string]string
	// InitOptions is sent in initialize, and Settings by
	// workspace/didChangeConfiguration and in answer to
	// workspace/configuration.
	InitOptions json.RawMessage
	Settings    json.RawMessage
	// WorkspaceFolder is the folder the server works on: the project's
	// root when empty.
	WorkspaceFolder string
	// StartupTimeout bounds the start (0: 30 seconds), ShutdownTimeout the
	// polite end (0: 2 seconds, then the process is stopped), and
	// RequestTimeout one question (0: a minute).
	StartupTimeout, ShutdownTimeout, RequestTimeout time.Duration
	// RestartOnCrash starts it again after it has stopped on its own, at
	// most MaxRestarts times (-1: no limit).
	RestartOnCrash bool
	MaxRestarts    int
	// Diagnostics gives the model the errors and warnings a change brings.
	Diagnostics bool
	Origin      Origin
}

// lspFields are the fields of an lspServers entry.
var lspFields = []keyDef{
	{"command", "", "the server's program", fromClaudeCode},
	{"args", "", "its arguments", fromClaudeCode},
	{"extensionToLanguage", "", "file endings, each with the language the server is told", fromClaudeCode},
	{"transport", "", "\"stdio\"; \"socket\" is taken and runs over stdio, as in Claude Code", fromClaudeCode},
	{"env", "", "variables for it", fromClaudeCode},
	{"initializationOptions", "", "sent in initialize", fromClaudeCode},
	{"settings", "", "sent as its configuration", fromClaudeCode},
	{"workspaceFolder", "", "the folder it works on", fromClaudeCode},
	{"startupTimeout", "", "milliseconds its start may take", fromClaudeCode},
	{"shutdownTimeout", "", "milliseconds its polite end may take", fromClaudeCode},
	{"requestTimeout", "", "milliseconds one question may take", fromClaudeCode},
	{"restartOnCrash", "", "start it again when it stops on its own", fromClaudeCode},
	{"maxRestarts", "", "how many times", fromClaudeCode},
	{"diagnostics", "", "give the model the problems a change brings", fromClaudeCode},
}

// lspServers reads the lspServers object: a server's name, then its
// entry, or null to take away a server a weaker file named.
func (s *source) lspServers(f jfield) {
	if f.val.kind != jObject {
		s.errorf(f.line, "\"lspServers\" is an object with one member for each language server, as in \"lspServers\": {\"gopls\": {\"command\": \"gopls\", \"args\": [\"serve\"], \"extensionToLanguage\": {\".go\": \"go\"}}}. Here it is %s, so none of it is in force.", f.val.kind.word())
		return
	}
	for _, m := range s.unique(f.val, "lspServers.") {
		name, o := m.key, s.at(m.line)
		at := "lspServers." + clip(name, 64)
		switch m.val.kind {
		case jNull:
			s.add(op{kind: KindRemoval, text: at + ": null (takes the server away)", origin: o, what: "taking a language server away",
				apply: func(b *builder) { delete(b.lsp, name) }})
			continue
		case jObject:
		default:
			s.errorf(m.line, "a language server is an object with \"command\" and \"extensionToLanguage\", or null to take away one a weaker file named. Here %s is %s.", showName(name), shown(m.val))
			continue
		}
		mark := len(s.probs.list)
		srv := LSPServer{Name: name, RestartOnCrash: true, MaxRestarts: -1, Diagnostics: true, Origin: o}
		ms := func(fl jfield) time.Duration {
			n, whole := fl.val.whole()
			if !whole || n < 1 {
				s.errorf(fl.line, "%s.%s is a whole number of milliseconds, at least 1, and here it is %s.", at, fl.key, shown(fl.val))
				return 0
			}
			return time.Duration(n) * time.Millisecond
		}
		truth := func(fl jfield) bool {
			if fl.val.kind != jBool {
				s.errorf(fl.line, "%s.%s is true or false, and here it is %s.", at, fl.key, shown(fl.val))
			}
			return fl.val.b
		}
		for _, fl := range s.unique(m.val, at+".") {
			switch fl.key {
			case "command", "workspaceFolder", "transport":
				if fl.val.kind != jString || strings.TrimSpace(fl.val.str) == "" {
					s.errorf(fl.line, "%s.%s is a string, and here it is %s.", at, fl.key, shown(fl.val))
					continue
				}
				switch v := strings.TrimSpace(fl.val.str); fl.key {
				case "command":
					if strings.ContainsAny(v, " \t") && !strings.HasPrefix(v, "/") {
						s.errorf(fl.line, "%s.command is the program alone, with no spaces unless it is a path that starts with /: put its arguments in \"args\".", at)
						continue
					}
					if strings.HasPrefix(v, "~/") {
						v = absPath(v, s.home, s.cwd)
					}
					if strings.Contains(v, "/") && !strings.HasPrefix(v, "/") {
						// (A path of the folder's would run the folder's own
						// file, which its repository can put there: the
						// language servers review of 2026-11-20.)
						s.errorf(fl.line, "%s.command is a program found on the PATH, by its name alone, or a path that starts with / or ~/; %s is neither.", at, showName(v))
						continue
					}
					srv.Command = v
				case "workspaceFolder":
					srv.WorkspaceFolder = absPath(v, s.home, s.cwd)
				default:
					if v != "stdio" && v != "socket" {
						s.errorf(fl.line, "%s.transport is \"stdio\" or \"socket\", and here it is %s.", at, showName(v))
					}
				}
			case "args":
				list, ok := fl.val.strings()
				if !ok {
					s.errorf(fl.line, "%s.args is a list of strings.", at)
					continue
				}
				for _, a := range list {
					srv.Args = append(srv.Args, a.str)
				}
			case "env", "extensionToLanguage":
				if fl.val.kind != jObject {
					s.errorf(fl.line, "%s.%s is an object of strings, and here it is %s.", at, fl.key, shown(fl.val))
					continue
				}
				pairs := map[string]string{}
				for _, kv := range s.unique(fl.val, at+"."+fl.key+".") {
					if kv.val.kind != jString {
						s.errorf(kv.line, "%s.%s.%s is a string, and here it is %s.", at, fl.key, clip(kv.key, 40), shown(kv.val))
						continue
					}
					k := kv.key
					if fl.key == "extensionToLanguage" {
						if !strings.HasPrefix(k, ".") || len(k) < 2 || strings.TrimSpace(kv.val.str) == "" {
							s.errorf(kv.line, "each key of %s.extensionToLanguage is a file ending that starts with \".\", such as \".go\", with the language for it, such as \"go\".", at)
							continue
						}
						k = strings.ToLower(k)
					}
					pairs[k] = kv.val.str
				}
				if fl.key == "env" {
					srv.Env = pairs
				} else {
					srv.Extensions = pairs
				}
			case "initializationOptions", "settings":
				raw, err := json.Marshal(s.plain(fl.val, at+"."+fl.key+"."))
				if err != nil {
					s.errorf(fl.line, "%s.%s cannot be passed on: %v.", at, fl.key, err)
					continue
				}
				if fl.key == "settings" {
					srv.Settings = raw
				} else {
					srv.InitOptions = raw
				}
			case "startupTimeout":
				srv.StartupTimeout = ms(fl)
			case "shutdownTimeout":
				srv.ShutdownTimeout = ms(fl)
			case "requestTimeout":
				srv.RequestTimeout = ms(fl)
			case "restartOnCrash":
				srv.RestartOnCrash = truth(fl)
			case "diagnostics":
				srv.Diagnostics = truth(fl)
			case "maxRestarts":
				n, whole := fl.val.whole()
				if !whole || n < 0 {
					s.errorf(fl.line, "%s.maxRestarts is a whole number, 0 or more, and here it is %s.", at, shown(fl.val))
					continue
				}
				srv.MaxRestarts = n
			default:
				// (Claude Code's entries are strict: an unknown field is a
				// mistake, not something passed over.)
				s.errorf(fl.line, "%s is not a field of a language server. The fields are: %s.", showName(fl.key), keyNames(lspFields))
			}
		}
		switch {
		case s.failed(mark):
			continue
		case srv.Command == "":
			s.errorf(m.line, "the language server %s has no \"command\".", showName(name))
			continue
		case len(srv.Extensions) == 0:
			s.errorf(m.line, "the language server %s has no \"extensionToLanguage\": name the file endings it is for, as in {\".go\": \"go\"}.", showName(name))
			continue
		}
		// The trust question says all that it would run: the command
		// line, and the names of the variables it would be given.
		exts := slices.Sorted(maps.Keys(srv.Extensions))
		text := "language server " + name + " for " + strings.Join(exts, " ") + ", run as " + strings.Join(append([]string{srv.Command}, srv.Args...), " ")
		if len(srv.Env) > 0 {
			text += " with " + strings.Join(slices.Sorted(maps.Keys(srv.Env)), ", ") + " set"
		}
		s.add(op{kind: KindLSPServer, text: text, origin: o, what: "a language server",
			apply: func(b *builder) {
				if b.lsp == nil {
					b.lsp = map[string]LSPServer{}
				}
				b.lsp[name] = srv
			}})
	}
}

// lspServerList is the servers in force, by name. Where two name the same
// ending, the first by name is the one for it, as in Claude Code the first
// registered is; the other is said to be passed over for it.
func (ld *loader) lspServerList() []LSPServer {
	names := slices.Sorted(maps.Keys(ld.b.lsp))
	out := make([]LSPServer, 0, len(names))
	taken := map[string]string{}
	for _, name := range names {
		srv := ld.b.lsp[name]
		for ext := range srv.Extensions {
			if first, ok := taken[ext]; ok {
				ld.probs.at(Warning, srv.Origin, "the language server %s is not used for %s files: %s is, being first by name.", name, ext, first)
				continue
			}
			taken[ext] = name
		}
		out = append(out, srv)
	}
	return out
}
