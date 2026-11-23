package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealGopls runs the client against gopls itself, the server gnosh is
// most used with, when GNOSH_GOPLS names it (go install
// golang.org/x/tools/gopls@latest): a type error is published after a
// change and gone after the fix, and definition, hover, references and
// the file's symbols answer. It is skipped otherwise: gopls is no part of
// the build.
func TestRealGopls(t *testing.T) {
	gopls := os.Getenv("GNOSH_GOPLS")
	if gopls == "" {
		t.Skip("GNOSH_GOPLS names no gopls")
	}
	root := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	main := write("main.go", "package main\n\nfunc greet() string { return \"hi\" }\n\nfunc main() {\n\tprintln(greet())\n}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := Start(ctx, Config{Name: "gopls", Command: gopls, Args: []string{"serve"}, Dir: root, Root: root,
		Env: append(os.Environ(), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	s, err := c.Sync(ctx, main, "go", "package main\n\nfunc greet() string { return \"hi\" }\n\nfunc main() {\n\tprintln(greet())\n}\n")
	if err != nil || !c.Settle(ctx, main, s, time.Second) {
		t.Fatalf("open: %+v, %v", s, err)
	}
	if d := c.Diagnostics()[main]; len(d) != 0 {
		t.Errorf("a clean file has diagnostics: %+v", d)
	}
	broken := "package main\n\nfunc greet() string { return \"hi\" }\n\nfunc main() {\n\tprintln(greet() + undefinedThing)\n}\n"
	s, err = c.Sync(ctx, main, "go", broken)
	if err != nil || !c.Settle(ctx, main, s, time.Second) {
		t.Fatalf("change: %+v, %v", s, err)
	}
	d := c.Diagnostics()[main]
	if len(d) == 0 || !strings.Contains(d[0].Message, "undefinedThing") || d[0].Range.Start.Line != 5 {
		t.Errorf("the type error: %+v", d)
	}

	// At greet's call, line 6 (from 1), character 10.
	at := FromRunes(6, 10, "\tprintln(greet() + undefinedThing)")
	locs, err := c.Definition(ctx, main, at)
	if err != nil || len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("definition: %+v, %v", locs, err)
	}
	if h, err := c.Hover(ctx, main, at); err != nil || !strings.Contains(h, "func greet() string") {
		t.Errorf("hover: %q, %v", h, err)
	}
	if refs, err := c.References(ctx, main, at); err != nil || len(refs) != 2 {
		t.Errorf("references: %+v, %v", refs, err)
	}
	syms, err := c.DocumentSymbols(ctx, main)
	names := []string{}
	for _, s := range syms {
		names = append(names, s.Name)
	}
	if err != nil || strings.Join(names, " ") != "greet main" {
		t.Errorf("symbols: %v, %v", names, err)
	}

	// The fix takes the error away.
	s, err = c.Sync(ctx, main, "go", strings.Replace(broken, " + undefinedThing", "", 1))
	if err != nil || !c.Settle(ctx, main, s, time.Second) {
		t.Fatalf("fix: %+v, %v", s, err)
	}
	if d := c.Diagnostics()[main]; len(d) != 0 {
		t.Errorf("after the fix: %+v", d)
	}
}
