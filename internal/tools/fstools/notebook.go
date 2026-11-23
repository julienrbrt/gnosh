package fstools

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file is Read for Jupyter notebooks, and the notebook that
// NotebookEdit changes. Claude Code's Read gives a notebook's cells with
// their outputs, pictures among them, and refuses one whose cells come to
// more than it shows (its tools reference, "Read tool behavior"). gnosh
// gives each cell under a line that names it (its number, its id, its
// kind, its run), then its outputs; a picture an output holds comes as a
// picture. NotebookEdit names cells by the ids this shows.

const (
	// nbMaxBytes is the largest notebook file Read or NotebookEdit takes.
	nbMaxBytes = 64 << 20
	// nbOutputChars is the most of one output's text that Read shows.
	nbOutputChars = 4000
	// nbTracebackLines is the most lines of an error's traceback shown.
	nbTracebackLines = 20
	// nbPictures is the most pictures one Read gives.
	nbPictures = 4
)

// nbDoc is a notebook as read: its JSON whole, numbers as written, so that
// what NotebookEdit does not touch goes back as it was.
type nbDoc struct {
	root  map[string]any
	cells []any
	// bom says the file began with a byte order mark, which it keeps.
	bom bool
}

// parseNotebook reads a notebook of nbformat 4.
func parseNotebook(data []byte) (*nbDoc, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte(utf8BOM))))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("it is not the JSON of a notebook (%v)", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("it holds more than the one JSON object of a notebook")
	}
	if _, old := root["worksheets"]; old {
		return nil, errors.New("it is a notebook of nbformat 3, which only nbformat 4 readers take: convert it with jupyter nbconvert --to notebook --nbformat 4")
	}
	cells, ok := root["cells"].([]any)
	if !ok {
		return nil, errors.New("it has no list of cells, so it is no Jupyter notebook")
	}
	for i, c := range cells {
		cell, ok := c.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("its cell %d is not an object", i+1)
		}
		if _, ok := cell["cell_type"].(string); !ok {
			return nil, fmt.Errorf("its cell %d says no cell_type", i+1)
		}
	}
	return &nbDoc{root: root, cells: cells, bom: bytes.HasPrefix(data, []byte(utf8BOM))}, nil
}

// language is the notebook's language, as its metadata names it, or "".
func (d *nbDoc) language() string {
	meta, _ := d.root["metadata"].(map[string]any)
	if info, ok := meta["language_info"].(map[string]any); ok {
		if name, ok := info["name"].(string); ok {
			return name
		}
	}
	if spec, ok := meta["kernelspec"].(map[string]any); ok {
		if name, ok := spec["language"].(string); ok {
			return name
		}
	}
	return ""
}

// cell is the cell at i.
func (d *nbDoc) cell(i int) map[string]any { return d.cells[i].(map[string]any) }

// cellName is how a cell is named to the model, and found again by
// NotebookEdit: its id, or "cell-N" (N from 1) when it has none, as cells
// written before nbformat 4.5 do not.
func (d *nbDoc) cellName(i int) string {
	if id, ok := d.cell(i)["id"].(string); ok && id != "" {
		return id
	}
	return fmt.Sprintf("cell-%d", i+1)
}

// find is the index of the cell named name, or -1.
func (d *nbDoc) find(name string) int {
	for i := range d.cells {
		if d.cellName(i) == name {
			return i
		}
	}
	return -1
}

// usesIDs reports whether the notebook's cells carry ids (nbformat 4.5 and
// later), so that a new cell needs one.
func (d *nbDoc) usesIDs() bool {
	if n, ok := d.root["nbformat_minor"].(json.Number); ok {
		if v, err := n.Int64(); err == nil && v >= 5 {
			return true
		}
	}
	for i := range d.cells {
		if _, ok := d.cell(i)["id"]; ok {
			return true
		}
	}
	return false
}

// newID is an id no cell of the notebook has.
func (d *nbDoc) newID() string {
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return fmt.Sprintf("cell-%d", len(d.cells)+1)
		}
		id := hex.EncodeToString(b[:])
		if d.find(id) < 0 {
			return id
		}
	}
}

// format writes the notebook as Jupyter writes one: keys in order, one
// space of indent, a line break at the end, characters as they are.
func (d *nbDoc) format() (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(d.root); err != nil {
		return "", err
	}
	// Go's encoder writes U+2028 and U+2029 escaped, which Jupyter does
	// not: they go back as the characters (JSON allows them in a string).
	out := lineSeparatorsBack(b.String())
	if d.bom {
		out = utf8BOM + out
	}
	return out, nil
}

// nbText joins a notebook text field: a string, or a list of strings.
func nbText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		var b strings.Builder
		for _, part := range t {
			if s, ok := part.(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

// sourceLines is a cell's source as Jupyter keeps it: a list of lines,
// each with its line break but the last.
func sourceLines(text string) []any {
	out := []any{}
	for len(text) > 0 {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:i+1])
		text = text[i+1:]
	}
	return out
}

// nbView is a notebook as Read gives it.
type nbView struct {
	text     string
	pictures []core.Block
	// left counts the pictures, and the outputs, left out.
	leftPictures int
}

// view lays the notebook out for the model, with its outputs or without.
func (d *nbDoc) view(o *Options, path string, outputs bool) nbView {
	var v nbView
	var b strings.Builder
	lang := d.language()
	if lang != "" {
		lang = " (" + lang + ")"
	}
	fmt.Fprintf(&b, "[%s is a Jupyter notebook%s of %s.]\n", path, lang, countOf(len(d.cells), "cell", "cells"))
	lines := func(text string) {
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			s, _ := showable([]byte(line))
			if kept, dropped := cutChars([]byte(s), o.ReadMaxLineChars); dropped > 0 {
				s = string(kept) + cutMark(dropped)
			}
			b.WriteString(s + "\n")
		}
	}
	for i := range d.cells {
		cell := d.cell(i)
		kind, _ := cell["cell_type"].(string)
		head := fmt.Sprintf("[cell %d · id %s · %s", i+1, d.cellName(i), kind)
		if kind == "code" {
			if n, ok := cell["execution_count"].(json.Number); ok {
				head += " · run " + n.String()
			} else {
				head += " · not run"
			}
		}
		b.WriteString(head + "]\n")
		if src := nbText(cell["source"]); src != "" {
			lines(src)
		}
		outs, _ := cell["outputs"].([]any)
		if kind != "code" || len(outs) == 0 {
			continue
		}
		if !outputs {
			fmt.Fprintf(&b, "[%s of cell %d left out]\n", countOf(len(outs), "output", "outputs"), i+1)
			continue
		}
		fmt.Fprintf(&b, "[output of cell %d]\n", i+1)
		for _, out := range outs {
			text := d.outputText(o, out, &v)
			if n := utf8.RuneCountInString(text); n > nbOutputChars {
				text = clipRunes(text, nbOutputChars) + fmt.Sprintf("\n[... %d more characters of this output]", n-nbOutputChars)
			}
			if text != "" {
				lines(text)
			}
		}
	}
	v.text = b.String()
	return v
}

// outputText is what one output says, as text; a picture in it is kept in
// v, while there is room for one.
func (d *nbDoc) outputText(o *Options, out any, v *nbView) string {
	m, ok := out.(map[string]any)
	if !ok {
		return ""
	}
	switch kind, _ := m["output_type"].(string); kind {
	case "stream":
		text := nbText(m["text"])
		if name, _ := m["name"].(string); name == "stderr" && text != "" {
			return "[stderr]\n" + text
		}
		return text
	case "error":
		name, _ := m["ename"].(string)
		value, _ := m["evalue"].(string)
		var tb []string
		if lines, ok := m["traceback"].([]any); ok {
			for _, l := range lines {
				if s, ok := l.(string); ok {
					tb = append(tb, strings.Split(stripEscapes(s), "\n")...)
				}
			}
		}
		if len(tb) > nbTracebackLines {
			tb = append([]string{fmt.Sprintf("[... %d lines of the traceback before these]", len(tb)-nbTracebackLines)}, tb[len(tb)-nbTracebackLines:]...)
		}
		return strings.TrimSpace(fmt.Sprintf("[error] %s: %s\n%s", name, value, strings.Join(tb, "\n")))
	case "execute_result", "display_data":
		data, _ := m["data"].(map[string]any)
		var parts []string
		for _, media := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
			raw, ok := data[media]
			if !ok {
				continue
			}
			pic, err := base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
				if r == '\n' || r == '\r' || r == ' ' {
					return -1
				}
				return r
			}, nbText(raw)))
			if err == nil && imageType(pic) != media {
				// What the bytes are, not what the notebook says they are:
				// a provider refuses a picture of another kind than named.
				if actual := imageType(pic); actual != "" {
					media = actual
				} else {
					err = errors.New("not a picture")
				}
			}
			switch {
			case err != nil:
				parts = append(parts, fmt.Sprintf("[a picture, %s, that cannot be decoded]", media))
			case len(v.pictures) >= nbPictures || int64(len(pic)) > o.ReadMaxImageBytes:
				v.leftPictures++
				parts = append(parts, fmt.Sprintf("[a picture, %s, %s, left out]", media, byteSize(int64(len(pic)))))
			default:
				v.pictures = append(v.pictures, core.Block{Type: core.BlockImage, MediaType: media, Data: pic})
				parts = append(parts, fmt.Sprintf("[picture %d, %s, given below]", len(v.pictures), media))
			}
			break
		}
		switch {
		case data["text/plain"] != nil:
			parts = append(parts, nbText(data["text/plain"]))
		case data["text/markdown"] != nil:
			parts = append(parts, nbText(data["text/markdown"]))
		case data["application/json"] != nil:
			if js, err := json.Marshal(data["application/json"]); err == nil {
				parts = append(parts, string(js))
			}
		case data["text/html"] != nil && len(parts) == 0:
			parts = append(parts, fmt.Sprintf("[HTML output, %s, left out]", countOf(utf8.RuneCountInString(nbText(data["text/html"])), "character", "characters")))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// stripEscapes takes out the terminal colour codes a traceback is written
// with.
func stripEscapes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// clipRunes is at most n characters of s.
func clipRunes(s string, n int) string {
	i := 0
	for at := range s {
		if i == n {
			return s[:at]
		}
		i++
	}
	return s
}

// readNotebook gives a notebook's cells and outputs.
func (t *readTool) readNotebook(ctx context.Context, env *core.ToolEnv, loc *location, file *os.File, info os.FileInfo, head []byte, atEnd bool) (core.ToolResult, error) {
	o := t.opts
	// The most one call shows: Read's own limit, or what one result may
	// hold for the model where that is less (ReadResultMost), since a
	// notebook is recorded as read whole (the blocks review of
	// 2026-11-20).
	most := o.ReadMaxBytes
	if o.ReadResultMost != nil {
		if m := o.ReadResultMost(ctx); m > 0 && m < most {
			most = m
		}
	}
	if info.Size() > nbMaxBytes {
		return refused(failf("%s is a notebook of %s, more than the %s %s reads. Look at some of its cells with a shell command (jq '.cells[0:10]' on it, for one).", loc.abs, byteSize(info.Size()), byteSize(nbMaxBytes), o.ReadName))
	}
	data := append([]byte(nil), head...)
	if !atEnd {
		rest, err := io.ReadAll(io.LimitReader(file, nbMaxBytes))
		if err != nil {
			return refused(failf("%s could not be read: %v.", loc.abs, underlying(err)))
		}
		data = append(data, rest...)
	}
	doc, err := parseNotebook(data)
	if err != nil {
		return refused(failf("%s could not be read as a notebook: %v. Look at it with a shell command.", loc.abs, err))
	}
	v := doc.view(o, loc.abs, true)
	note := ""
	if len(v.text) > most {
		v = doc.view(o, loc.abs, false)
		note = "[Its outputs were left out: with them it is more than one call shows.]\n"
	}
	if len(v.text) > most {
		return refused(failf("%s is a notebook of %s whose cells alone come to %s, more than the %s one call shows. Look at some of its cells with a shell command (jq '.cells[0:10]' on it, for one), or find what you need with %s.",
			loc.abs, countOf(len(doc.cells), "cell", "cells"), byteSize(int64(len(v.text))), byteSize(int64(most)), o.GrepName))
	}
	sum := sha256.Sum256(data)
	sessionOf(env).saw(loc.real, sighting{info: info, sum: &sum, lines: -1, whole: true})
	res := core.ToolResult{Content: append([]core.Block{core.Text(v.text + note)}, v.pictures...)}
	res.Meta = map[string]any{
		"type": "notebook",
		"file": map[string]any{"filePath": loc.abs, "cells": len(doc.cells), "pictures": len(v.pictures)},
	}
	if loc.real != loc.abs {
		res.Meta["realPath"] = loc.real
	}
	return res, nil
}

// lineSeparatorsBack writes U+2028 and U+2029 back as themselves where
// Go's encoder escaped them, and leaves every other escape as it is: a
// backslash escaped before "u2028" is text, not an escape.
func lineSeparatorsBack(s string) string {
	if !strings.Contains(s, `\u202`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if rest := s[i+1:]; strings.HasPrefix(rest, "u2028") || strings.HasPrefix(rest, "u2029") {
			b.WriteString(map[string]string{"u2028": "\u2028", "u2029": "\u2029"}[rest[:5]])
			i += 5
			continue
		}
		b.WriteByte(s[i])
		b.WriteByte(s[i+1])
		i++
	}
	return b.String()
}
