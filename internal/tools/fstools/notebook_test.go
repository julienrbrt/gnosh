package fstools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// sampleNotebook is a notebook as Jupyter writes one (nbformat 4.5): a
// markdown cell, a code cell that ran with text and a picture among its
// outputs, a code cell that failed, and one never run.
func sampleNotebook(t *testing.T) string {
	t.Helper()
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00"))
	nb := map[string]any{
		"cells": []any{
			map[string]any{"cell_type": "markdown", "id": "intro01", "metadata": map[string]any{}, "source": []any{"# Sums\n", "Adding numbers."}},
			map[string]any{"cell_type": "code", "id": "sum0002", "execution_count": 3, "metadata": map[string]any{}, "source": []any{"print(1 + 2)\n", "plot()"},
				"outputs": []any{
					map[string]any{"output_type": "stream", "name": "stdout", "text": []any{"3\n"}},
					map[string]any{"output_type": "display_data", "metadata": map[string]any{}, "data": map[string]any{"image/png": png, "text/plain": []any{"<Figure size 640x480>"}}},
				}},
			map[string]any{"cell_type": "code", "id": "boom003", "execution_count": 4, "metadata": map[string]any{}, "source": "x",
				"outputs": []any{map[string]any{"output_type": "error", "ename": "NameError", "evalue": "name 'x' is not defined", "traceback": []any{"\x1b[0;31mNameError\x1b[0m: name 'x' is not defined"}}}},
			map[string]any{"cell_type": "code", "id": "todo004", "execution_count": nil, "metadata": map[string]any{}, "source": []any{}, "outputs": []any{}},
		},
		"metadata":       map[string]any{"language_info": map[string]any{"name": "python"}},
		"nbformat":       4,
		"nbformat_minor": 5,
	}
	data, err := json.MarshalIndent(nb, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// Read gives a notebook's cells, each named by its id, with their outputs:
// text, a picture as a picture, an error with its traceback cleaned.
func TestReadNotebook(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("sums.ipynb", sampleNotebook(t))
	res := f.run(f.read, in{"file_path": p})
	if res.IsError {
		t.Fatalf("failed: %s", core.TextOf(res.Content))
	}
	text := core.TextOf(res.Content)
	mustContain(t, text,
		"is a Jupyter notebook (python) of 4 cells.]",
		"[cell 1 · id intro01 · markdown]\n# Sums\nAdding numbers.\n",
		"[cell 2 · id sum0002 · code · run 3]\nprint(1 + 2)\nplot()\n[output of cell 2]\n3\n[picture 1, image/png, given below]\n<Figure size 640x480>\n",
		"[cell 3 · id boom003 · code · run 4]\nx\n[output of cell 3]\n[error] NameError: name 'x' is not defined\nNameError: name 'x' is not defined\n",
		"[cell 4 · id todo004 · code · not run]\n")
	pictures := 0
	for _, b := range res.Content {
		if b.Type == core.BlockImage && b.MediaType == "image/png" {
			pictures++
		}
	}
	if pictures != 1 {
		t.Errorf("%d pictures came back, want 1", pictures)
	}
	if res.Meta["type"] != "notebook" {
		t.Errorf("meta: %v", res.Meta)
	}
}

// NotebookEdit replaces, inserts and deletes cells by id, wants the
// notebook read first and unchanged since, and writes it back as Jupyter
// does: a replaced code cell loses its outputs and run count, and what it
// does not touch stays as it was.
func TestNotebookEdit(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("sums.ipynb", sampleNotebook(t))
	f.fails(f.nbedit, in{"notebook_path": p, "cell_id": "sum0002", "new_source": "print(2)"}, "has not been read", "Read it with Read first")
	f.ok(f.read, in{"file_path": p})

	out := f.ok(f.nbedit, in{"notebook_path": p, "cell_id": "sum0002", "new_source": "print(2 + 2)\nshow()"})
	mustContain(t, out, "Replaced the source of cell sum0002 (cell 2 of 4)", "outputs and run count were cleared")
	doc := readNB(t, f, p)
	cell := doc.cell(1)
	if got := nbText(cell["source"]); got != "print(2 + 2)\nshow()" {
		t.Errorf("source = %q", got)
	}
	if outs, _ := cell["outputs"].([]any); len(outs) != 0 || cell["execution_count"] != nil {
		t.Errorf("outputs %v, run %v left", cell["outputs"], cell["execution_count"])
	}
	if src, ok := cell["source"].([]any); !ok || len(src) != 2 || src[0] != "print(2 + 2)\n" {
		t.Errorf("source kept as %#v, want Jupyter's list of lines", cell["source"])
	}
	// The notebook as Jupyter writes it: one space of indent, keys in
	// order, untouched cells as they were.
	raw := f.content(p)
	if !strings.HasPrefix(raw, "{\n \"cells\": [\n  {\n") || !strings.HasSuffix(raw, "}\n") {
		t.Errorf("not in Jupyter's form:\n%s", raw[:min(200, len(raw))])
	}
	mustContain(t, raw, `"name 'x' is not defined"`, `"nbformat_minor": 5`)

	out = f.ok(f.nbedit, in{"notebook_path": p, "cell_id": "intro01", "new_source": "import math", "cell_type": "code", "edit_mode": "insert"})
	mustContain(t, out, "Inserted a code cell, id ", "as cell 2 of 5")
	doc = readNB(t, f, p)
	if doc.cell(1)["cell_type"] != "code" || nbText(doc.cell(1)["source"]) != "import math" || doc.cellName(1) == "cell-2" {
		t.Errorf("the inserted cell: %v", doc.cell(1))
	}
	f.ok(f.nbedit, in{"notebook_path": p, "new_source": "# Top", "cell_type": "markdown", "edit_mode": "insert"})
	if doc = readNB(t, f, p); nbText(doc.cell(0)["source"]) != "# Top" {
		t.Errorf("a cell inserted with no cell_id is not at the top: %v", doc.cell(0))
	}
	out = f.ok(f.nbedit, in{"notebook_path": p, "cell_id": "boom003", "new_source": "", "edit_mode": "delete"})
	mustContain(t, out, "Deleted cell boom003", "now has 5 cells")
	if readNB(t, f, p).find("boom003") >= 0 {
		t.Error("the deleted cell is still there")
	}
	// A markdown cell made code gains outputs; made markdown, loses them.
	f.ok(f.nbedit, in{"notebook_path": p, "cell_id": "todo004", "new_source": "Notes", "cell_type": "markdown"})
	if c := readNB(t, f, p).cell(4); c["cell_type"] != "markdown" || c["outputs"] != nil || c["execution_count"] != nil {
		t.Errorf("a code cell made markdown: %v", c)
	}

	f.fails(f.nbedit, in{"notebook_path": p, "cell_id": "nope", "new_source": "x"}, `has no cell with the id "nope"`, "intro01")
	f.fails(f.nbedit, in{"notebook_path": p, "new_source": "x", "edit_mode": "insert"}, "needs its cell_type")
	f.fails(f.nbedit, in{"notebook_path": p, "new_source": "x"}, "needs the cell_id")
	f.fails(f.nbedit, in{"notebook_path": p, "cell_id": "intro01", "new_source": "x", "edit_mode": "move"}, "replace, insert or delete")
	// Changed behind the session's back: read it again.
	f.file("sums.ipynb", sampleNotebook(t))
	f.fails(f.nbedit, in{"notebook_path": p, "cell_id": "intro01", "new_source": "x"}, "has changed on disk")
}

// A notebook written before nbformat 4.5 has no cell ids: its cells are
// named cell-N, and a cell inserted gets none.
func TestNotebookEditWithoutIDs(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("old.ipynb", `{"cells": [{"cell_type": "code", "execution_count": 1, "metadata": {}, "outputs": [], "source": ["a = 1"]}], "metadata": {}, "nbformat": 4, "nbformat_minor": 2}`+"\n")
	mustContain(t, f.ok(f.read, in{"file_path": p}), "[cell 1 · id cell-1 · code · run 1]\na = 1\n")
	f.ok(f.nbedit, in{"notebook_path": p, "cell_id": "cell-1", "new_source": "b = 2", "cell_type": "code", "edit_mode": "insert"})
	doc := readNB(t, f, p)
	if _, has := doc.cell(1)["id"]; has || len(doc.cells) != 2 {
		t.Errorf("the cell inserted into a notebook without ids: %v", doc.cell(1))
	}
}

func readNB(t *testing.T, f *fixture, p string) *nbDoc {
	t.Helper()
	doc, err := parseNotebook([]byte(f.content(p)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// The review of the day's code (2026-11-20): new_source is needed but to
// delete; a notebook without ids is read again after an insert; a line
// separator in a cell, and the text \u2028, and a byte order mark, come
// back as they were; a notebook that is not UTF-8 is not changed; a
// picture is sent as what its bytes are; Edit leaves notebooks to
// NotebookEdit.
func TestNotebookEditReviewed(t *testing.T) {
	f := newFixture(t, Options{})
	p := f.file("sums.ipynb", sampleNotebook(t))
	f.ok(f.read, in{"file_path": p})
	f.fails(f.nbedit, in{"notebook_path": p, "cell_id": "sum0002"}, "needs new_source")
	f.fails(f.edit, in{"file_path": p, "old_string": "print(1 + 2)", "new_string": "print(2)"}, "is a Jupyter notebook", "NotebookEdit")

	old := f.file("old.ipynb", `{"cells": [{"cell_type": "code", "execution_count": 1, "metadata": {}, "outputs": [], "source": ["a = 1"]}, {"cell_type": "code", "execution_count": 2, "metadata": {}, "outputs": [], "source": ["b = 2"]}], "metadata": {}, "nbformat": 4, "nbformat_minor": 2}`+"\n")
	f.ok(f.read, in{"file_path": old})
	f.ok(f.nbedit, in{"notebook_path": old, "new_source": "z = 0", "cell_type": "code", "edit_mode": "insert"})
	f.fails(f.nbedit, in{"notebook_path": old, "cell_id": "cell-2", "new_source": "x"}, "to be read again", "have moved")
	f.ok(f.read, in{"file_path": old})
	f.ok(f.nbedit, in{"notebook_path": old, "cell_id": "cell-2", "new_source": "a = 10"})
	if src := nbText(readNB(t, f, old).cell(1)["source"]); src != "a = 10" {
		t.Errorf("cell-2 after the read again holds %q", src)
	}
	// A replace in a notebook without ids moves nothing.
	f.ok(f.nbedit, in{"notebook_path": old, "cell_id": "cell-3", "new_source": "b = 20"})

	sep := "\uFEFF" + `{"cells": [{"cell_type": "markdown", "id": "a", "metadata": {}, "source": ["one` + "\u2028" + `two \\u2028 three"]}, {"cell_type": "markdown", "id": "b", "metadata": {}, "source": ["x"]}], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}` + "\n"
	q := f.file("sep.ipynb", sep)
	f.ok(f.read, in{"file_path": q})
	f.ok(f.nbedit, in{"notebook_path": q, "cell_id": "b", "new_source": "y"})
	got := f.content(q)
	if !strings.HasPrefix(got, "\uFEFF{") {
		t.Errorf("the byte order mark was lost: %q", got[:min(10, len(got))])
	}
	mustContain(t, got, "one\u2028two \\\\u2028 three")
	if src := nbText(readNB(t, f, q).cell(0)["source"]); src != "one\u2028two \\u2028 three" {
		t.Errorf("the cell with the line separator holds %q", src)
	}

	latin := f.file("latin.ipynb", "{\"cells\": [{\"cell_type\": \"markdown\", \"id\": \"a\", \"metadata\": {}, \"source\": [\"caf\xe9\"]}], \"metadata\": {}, \"nbformat\": 4, \"nbformat_minor\": 5}\n")
	f.ok(f.read, in{"file_path": latin})
	f.fails(f.nbedit, in{"notebook_path": latin, "cell_id": "a", "new_source": "x"}, "not valid UTF-8")

	jpeg := base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01"))
	pics := f.file("pics.ipynb", `{"cells": [{"cell_type": "code", "execution_count": 1, "id": "c", "metadata": {}, "source": [], "outputs": [`+
		`{"output_type": "display_data", "metadata": {}, "data": {"image/png": "`+jpeg+`"}},`+
		`{"output_type": "display_data", "metadata": {}, "data": {"image/png": "`+base64.StdEncoding.EncodeToString([]byte("not a picture"))+`"}}]}], "metadata": {}, "nbformat": 4, "nbformat_minor": 5}`+"\n")
	res := f.run(f.read, in{"file_path": pics})
	mustContain(t, core.TextOf(res.Content), "[picture 1, image/jpeg, given below]", "[a picture, image/png, that cannot be decoded]")
	for _, b := range res.Content {
		if b.Type == core.BlockImage && b.MediaType != "image/jpeg" {
			t.Errorf("a picture went as %s", b.MediaType)
		}
	}
}

// A notebook is shown whole or not at all, and recorded as read whole, so
// it keeps under what one result may hold for the model (ReadResultMost)
// as a text file does: with outputs left out first, and refused when its
// cells alone come to more (the blocks review of 2026-11-20).
func TestReadNotebookKeepsUnderTheResultCap(t *testing.T) {
	f := newFixture(t, Options{ReadResultMost: func(context.Context) int { return 420 }})
	p := f.file("sums.ipynb", sampleNotebook(t))
	got := f.ok(f.read, in{"file_path": p})
	mustContain(t, got, "Its outputs were left out")
	if strings.Contains(got, "NameError") {
		t.Error("the outputs were shown over the cap")
	}
	g := newFixture(t, Options{ReadResultMost: func(context.Context) int { return 100 }})
	q := g.file("sums.ipynb", sampleNotebook(t))
	g.fails(g.read, in{"file_path": q}, "whose cells alone come to", "the 100 bytes one call shows")
}
