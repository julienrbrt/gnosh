package fstools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// notebookEditTool is NotebookEdit, as Claude Code's (its tools reference,
// "NotebookEdit tool behavior"): one cell of a Jupyter notebook at a time,
// by its id, replaced, inserted after or deleted; a code cell replaced loses
// its outputs and its run count. Like Write it wants the notebook read, and
// unchanged since; like Edit it is judged by the rules of Edit.
type notebookEditTool struct {
	opts *Options
	sys  *fileOps
}

func (t *notebookEditTool) props() []prop {
	return []prop{
		{name: "notebook_path", kind: "string", desc: "Path of the .ipynb file. Absolute is best."},
		{name: "cell_id", kind: "string", desc: "The id of the cell, as Read shows it. For insert, the new cell goes after it; without it, at the top."},
		{name: "new_source", kind: "string", desc: "The cell's new source. For delete, give an empty string."},
		{name: "cell_type", kind: "string", desc: "code or markdown. Needed for insert; for replace, changes the cell's kind."},
		{name: "edit_mode", kind: "string", desc: "replace (the default), insert or delete."},
	}
}

func (t *notebookEditTool) Spec() core.ToolSpec {
	o := t.opts
	desc := fmt.Sprintf(`Changes one cell of a Jupyter notebook (.ipynb): replaces its source, inserts a new cell after it, or deletes it. Cells are named by the id %[1]s shows for each. Replacing a code cell clears its outputs and run count; run it again to have them back. Read the notebook first. For any other file use %[2]s.`, o.ReadName, o.EditName)
	return core.ToolSpec{Name: o.NotebookEditName, Description: desc, InputSchema: schema([]string{"notebook_path", "new_source"}, t.props()...)}
}

// nbEdit is a NotebookEdit call worked out: the change to the file, and
// what it does to which cell.
type nbEdit struct {
	*change
	mode, name, kind string
	// at is the index of the cell the edit leaves in place, or that went;
	// total is how many cells the notebook then has.
	at, total int
	// cleared says a code cell replaced had outputs or a run count.
	cleared bool
	// ids says the notebook's cells carry ids, which do not move.
	ids bool
}

// plan reads the call and the notebook, and makes the change.
func (t *notebookEditTool) plan(call core.ToolCall, env *core.ToolEnv) (*nbEdit, *failure) {
	o := t.opts
	name := o.NotebookEditName
	a, f := parseArgs(name, call.Input, propNames(t.props())...)
	if f != nil {
		return nil, f
	}
	path, f := a.required("notebook_path")
	if f != nil {
		return nil, f
	}
	source, gotSource, f := a.text("new_source")
	if f != nil {
		return nil, f
	}
	id, _, f := a.text("cell_id")
	if f != nil {
		return nil, f
	}
	kind, _, f := a.text("cell_type")
	if f != nil {
		return nil, f
	}
	mode, _, f := a.text("edit_mode")
	if f != nil {
		return nil, f
	}
	id, kind, mode = strings.TrimSpace(id), strings.TrimSpace(kind), strings.TrimSpace(mode)
	if mode == "" {
		mode = "replace"
	}
	switch {
	case mode != "replace" && mode != "insert" && mode != "delete":
		return nil, failf("%s: edit_mode is %q; it is replace, insert or delete. Nothing was changed.", name, mode)
	case kind != "" && kind != "code" && kind != "markdown":
		return nil, failf("%s: cell_type is %q; it is code or markdown. Nothing was changed.", name, kind)
	case mode == "insert" && kind == "":
		return nil, failf("%s: a cell inserted needs its cell_type, code or markdown. Nothing was changed.", name)
	case mode != "insert" && id == "":
		return nil, failf("%s: %s needs the cell_id of the cell, as %s shows it. Nothing was changed.", name, mode, o.ReadName)
	case mode != "delete" && !gotSource:
		// (Left out, it would have emptied the cell: the review of the
		// day's code, 2026-11-20.)
		return nil, failf("%s: %s needs new_source, the cell's new text. Nothing was changed.", name, mode)
	}
	loc, f := o.locateFile(env, name, path)
	if f != nil {
		return nil, f
	}
	e := &nbEdit{change: &change{loc: loc, shown: true}, mode: mode, name: id, kind: kind}
	e.summary = fmt.Sprintf("Edit the notebook %s", loc.abs)
	if loc.trouble != nil {
		return e, loc.trouble
	}
	if !loc.exists {
		return e, failf("%s does not exist. %s changes the cells of a notebook that is there; to make a new one, use %s.", loc.abs, name, o.WriteName)
	}
	if f := o.writable(name, &loc); f != nil {
		return e, f
	}
	rec, known := sessionOf(env).record(loc.real)
	if !known {
		return e, failf("%s has not been read in this session. Nothing was changed. Read it with %s first.", loc.abs, o.ReadName)
	}
	data, info, f := readWhole(loc.real, nbMaxBytes)
	if f != nil {
		return e, f
	}
	e.old = info
	if data == nil {
		return e, failf("%s is %s, more than the %s %s changes. Nothing was changed.", loc.abs, byteSize(info.Size()), byteSize(nbMaxBytes), name)
	}
	if !rec.unchanged(info, data) {
		return e, staleFailure(o, &loc)
	}
	if !utf8.Valid(data) {
		// As Claude Code's NotebookEdit refuses one: written back as
		// UTF-8, every byte that does not decode would be lost.
		return e, failf("%s is not valid UTF-8. Nothing was changed. Change it with a shell command that keeps its encoding.", loc.abs)
	}
	if !rec.all {
		return e, failf("%s is to be read again before it is changed: its cells have no ids, and the numbers of those after the last one inserted or deleted have moved. Nothing was changed. Read it with %s first.", loc.abs, o.ReadName)
	}
	doc, err := parseNotebook(data)
	if err != nil {
		return e, failf("%s cannot be changed as a notebook: %v. Nothing was changed.", loc.abs, err)
	}
	at := -1
	if id != "" {
		if at = doc.find(id); at < 0 {
			names := make([]string, 0, len(doc.cells))
			for i := range doc.cells {
				names = append(names, doc.cellName(i))
			}
			shown := strings.Join(names, ", ")
			if len(names) > 12 {
				shown = strings.Join(names[:12], ", ") + ", …"
			}
			return e, failf("%s has no cell with the id %q. Nothing was changed. Its cells are: %s.", loc.abs, id, shown)
		}
	}
	switch mode {
	case "replace":
		cell := doc.cell(at)
		if kind != "" && kind != cell["cell_type"] {
			cell["cell_type"] = kind
		}
		cell["source"] = sourceLines(source)
		if cell["cell_type"] == "code" {
			outs, _ := cell["outputs"].([]any)
			e.cleared = len(outs) > 0 || cell["execution_count"] != nil
			cell["outputs"] = []any{}
			cell["execution_count"] = nil
		} else {
			delete(cell, "outputs")
			delete(cell, "execution_count")
		}
		e.at = at
	case "insert":
		cell := map[string]any{"cell_type": kind, "metadata": map[string]any{}, "source": sourceLines(source)}
		if kind == "code" {
			cell["outputs"] = []any{}
			cell["execution_count"] = nil
		}
		if doc.usesIDs() {
			cell["id"] = doc.newID()
		}
		doc.cells = slices.Insert(doc.cells, at+1, any(cell))
		e.at = at + 1
	case "delete":
		doc.cells = slices.Delete(doc.cells, at, at+1)
		e.at = at
	}
	doc.root["cells"] = doc.cells
	if mode == "insert" {
		e.name = doc.cellName(e.at)
	}
	e.total = len(doc.cells)
	e.ids = doc.usesIDs()
	after, err := doc.format()
	if err != nil {
		return e, failf("%s could not be written out: %v. Nothing was changed.", loc.abs, err)
	}
	e.before, e.after = string(data), after
	if int64(len(e.before)+len(e.after)) > o.DiffMaxBytes {
		// (Both whole notebooks would go into the session's file with
		// every call: the review of the day's code, 2026-11-20.)
		e.shown = false
	}
	e.same = bytes.Equal(data, []byte(after))
	switch mode {
	case "replace":
		e.summary = fmt.Sprintf("Replace cell %s of the notebook %s", e.name, loc.abs)
	case "insert":
		e.summary = fmt.Sprintf("Insert a %s cell into the notebook %s", kind, loc.abs)
	case "delete":
		e.summary = fmt.Sprintf("Delete cell %s of the notebook %s", e.name, loc.abs)
	}
	return e, nil
}

// notebookArg reads just the notebook_path of a call and finds where it
// leads, as pathArg does for Write and Edit.
func notebookArg(o *Options, call core.ToolCall, env *core.ToolEnv) (loc location, ok bool) {
	var fields struct {
		Path *string `json:"notebook_path"`
	}
	if len(bytes.TrimSpace(call.Input)) == 0 || json.Unmarshal(call.Input, &fields) != nil || fields.Path == nil {
		return location{}, false
	}
	loc, f := o.locateFile(env, o.NotebookEditName, *fields.Path)
	if f != nil || loc.trouble != nil {
		return location{}, false
	}
	return loc, true
}

// Describe says what the call would change, for the permission check.
func (t *notebookEditTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	e, f := t.plan(call, env)
	if e == nil {
		return core.Action{Kind: core.ActionEdit}, asError(f)
	}
	if f != nil {
		return core.Action{Kind: core.ActionEdit, Paths: e.loc.paths(), Summary: e.summary}, f
	}
	t.sys.judge(call.ID, e.loc.real)
	return e.action(), nil
}

// Run makes the change.
func (t *notebookEditTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	o := t.opts
	name := o.NotebookEditName
	where, ok := notebookArg(o, call, env)
	if !ok {
		_, f := t.plan(call, env)
		return unusable(name, f)
	}
	unlock, err := lockFor(ctx, env, name, &where)
	if err != nil {
		return core.ToolResult{}, err
	}
	defer unlock()
	e, f := t.plan(call, env)
	if f != nil {
		return refused(f)
	}
	if e.loc.real != where.real {
		return refused(failf("%s leads somewhere else than it did a moment ago (%s, not %s). Nothing was changed. Run the call again if that is what you mean.", e.loc.abs, e.loc.real, where.real))
	}
	if was, moved := t.sys.judgedElsewhere(call.ID, e.loc.real); moved {
		return refused(failf("%s leads somewhere else than when the call was looked at (%s, not %s). Nothing was changed. Run the call again if that is what you mean.", e.loc.abs, e.loc.real, was))
	}
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, fmt.Errorf("%s of %s stopped before anything was written: %w", name, e.loc.abs, context.Cause(ctx))
	}
	meta := map[string]any{"type": "notebook", "filePath": e.loc.abs, "editMode": e.mode, "cellId": e.name, "cellType": e.kind, "cells": e.total}
	if e.same {
		res := core.TextResult(fmt.Sprintf("Cell %s of %s already holds exactly this. Nothing was written.", e.name, e.loc.abs))
		res.Meta = meta
		return res, nil
	}
	if f := o.beforeChange(env, e.change); f != nil {
		return refused(f)
	}
	info, _, f := commit(t.sys, e.change)
	if f != nil {
		return refused(f)
	}
	sum := sha256.Sum256([]byte(e.after))
	// Where cells have no ids their names are their numbers, and an
	// insert or a delete moves those of the cells after it: the notebook
	// is then to be read again before the next change, so that no cell is
	// named by a number that now belongs to another.
	seenWhole := e.mode == "replace" || e.ids
	sessionOf(env).wrote(e.loc.real, info, sum, seenWhole, nil, lineCount(e.after))

	var text string
	switch e.mode {
	case "replace":
		text = fmt.Sprintf("Replaced the source of cell %s (cell %d of %d) in %s.", e.name, e.at+1, e.total, e.loc.abs)
		if e.cleared {
			text += " Its outputs and run count were cleared: run it again to have them back."
		}
	case "insert":
		text = fmt.Sprintf("Inserted a %s cell, id %s, as cell %d of %d in %s.", e.kind, e.name, e.at+1, e.total, e.loc.abs)
	case "delete":
		text = fmt.Sprintf("Deleted cell %s from %s, which now has %s.", e.name, e.loc.abs, countOf(e.total, "cell", "cells"))
	}
	res := core.TextResult(text)
	res.Display = e.display()
	res.Meta = meta
	return res, nil
}
