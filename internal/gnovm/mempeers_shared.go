// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
)

// sessionKey is a session id as a file name: its hash, since a sub-agent's
// id holds the provider's tool-call id.
func sessionKey(session string) string {
	sum := sha256.Sum256([]byte(session))
	return hex.EncodeToString(sum[:12])
}

// turnLockPath is session's turn lock, under gnosh's gnovm data directory
// dir: the agent holds it from when the session gets busy until it goes
// idle, and the kernel drops it if the process dies.
func turnLockPath(dir, session string) string {
	return filepath.Join(dir, "turns", sessionKey(session)+".lock")
}

// endsWithBareNext reports whether code's last statement is mem.Next(),
// alone, and the input's only mem.Next(): the only input whose Next may
// wait, so an input never keeps or loses what came before its Next
// depending on what peers do, and one wait answers one Next.
func endsWithBareNext(code string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p\nfunc _() {\n"+code+"\n}\n", 0)
	if err != nil || len(f.Decls) != 1 {
		return false
	}
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil || len(fn.Body.List) == 0 {
		return false
	}
	es, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 || !isMemNext(call) {
		return false
	}
	nexts := 0
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && isMemNext(c) {
			nexts++
		}
		return true
	})
	return nexts == 1
}

// isMemNext reports whether call calls mem.Next.
func isMemNext(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Next" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "mem"
}
