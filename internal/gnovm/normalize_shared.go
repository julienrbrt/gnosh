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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// op is one step of a journal entry. Inputs are journaled as ops, not as the
// text the model sent, so replay runs exactly what ran, whatever later
// versions of the normalisation below do.
type op struct {
	// Import binds Name to the package at Path, package-wide.
	Import *importOp `json:"import,omitempty"`
	// File holds top-level declarations, run as a new file of the package:
	// declarations in one file may refer to each other in any order.
	File string `json:"file,omitempty"`
	// Stmts holds statements, run in order in the package block.
	Stmts string `json:"stmts,omitempty"`
	// Call names a function to call: an input's main, renamed.
	Call string `json:"call,omitempty"`
	// Eval is an expression whose value is shown.
	Eval string `json:"eval,omitempty"`
	// Line is the input line File or Stmts began on, so errors point at
	// the model's own lines.
	Line int `json:"line,omitempty"`
}

// declBytes is the source an op keeps in memory once it has run: a file's
// declarations, and the function literals in statements or an evaluation (a
// stored closure keeps its syntax tree; other statements keep nothing). The
// budget charges it (vmCharge) and the limits cap it (checkLimits).
func (o op) declBytes() int64 {
	switch {
	case o.File != "":
		return int64(len(o.File))
	case o.Stmts != "":
		return funcLitBytes(o.Stmts, true)
	case o.Eval != "":
		return funcLitBytes(o.Eval, false)
	}
	return 0
}

// funcLitBytes sums the source of the outermost function literals in
// statements (stmts) or an expression.
func funcLitBytes(src string, stmts bool) int64 {
	if !strings.Contains(src, "func") {
		return 0
	}
	fset := token.NewFileSet()
	var root ast.Node
	if stmts {
		f, err := parser.ParseFile(fset, "", "package p;func _(){"+src+"\n}", parser.SkipObjectResolution)
		if err != nil {
			return int64(len(src)) // cannot tell: count it all
		}
		root = f
	} else {
		x, err := parser.ParseExpr(src)
		if err != nil {
			return int64(len(src))
		}
		root = x
	}
	var n int64
	ast.Inspect(root, func(node ast.Node) bool {
		if fl, ok := node.(*ast.FuncLit); ok {
			n += int64(fl.End() - fl.Pos())
			return false
		}
		return true
	})
	return n
}

type importOp struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (i *importOp) source() string { return fmt.Sprintf("import %s %q", i.Name, i.Path) }
