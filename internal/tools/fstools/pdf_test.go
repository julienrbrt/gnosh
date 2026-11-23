package fstools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// A PDF is read as its text, page by page, by what reads PDFs (stand-ins
// for poppler's tools here): whole when it has at most 10 pages, else by
// pages, at most 20 a call; pages past what one call shows are left for
// the next; a PDF with no text says so; one that cannot be read, and
// poppler not installed, say what to do. A PDF is known by its first
// bytes, whatever its name, and by its name, whatever its bytes.
func TestReadPDF(t *testing.T) {
	pages := map[string]int{}
	var asked []string
	f := newFixture(t, Options{
		ReadMaxBytes: 2000,
		PDFPages: func(ctx context.Context, path string) (int, error) {
			switch filepath.Base(path) {
			case "broken.pdf":
				return 0, errors.New("pdfinfo: Syntax Error: Couldn't find trailer dictionary")
			case "none.pdf":
				return 0, fmt.Errorf("pdfinfo: %w", errNoPoppler)
			}
			return pages[filepath.Base(path)], nil
		},
		PDFText: func(ctx context.Context, path string, first, last int) (string, error) {
			name := filepath.Base(path)
			asked = append(asked, fmt.Sprintf("%s %d-%d", name, first, last))
			var b strings.Builder
			for p := first; p <= last; p++ {
				switch name {
				case "scan.pdf":
				case "big.pdf":
					b.WriteString(strings.Repeat(fmt.Sprintf("page %d words ", p), 40) + "\n")
				case "huge.pdf":
					b.WriteString(strings.Repeat("y", 3000) + "\n")
				default:
					fmt.Fprintf(&b, "Text of page %d.   \n\n", p)
				}
				b.WriteString("\f")
			}
			return b.String(), nil
		},
	})
	pdf := func(name string, n int) string {
		pages[name] = n
		return f.file(name, "%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	}

	short := pdf("short.pdf", 3)
	mustContain(t, f.ok(f.read, in{"file_path": short}),
		"is a PDF of 3 pages; here is the text of pages 1-3.]\n[page 1]\nText of page 1.\n[page 2]\nText of page 2.\n[page 3]\nText of page 3.\n")
	res := f.run(f.read, in{"file_path": short, "pages": "2"})
	mustContain(t, core.TextOf(res.Content), "here is the text of page 2.]\n[page 2]\nText of page 2.\n")
	file, _ := res.Meta["file"].(map[string]any)
	if res.Meta["type"] != "pdf" || file["numPages"] != 3 || file["firstPage"] != 2 || file["lastPage"] != 2 {
		t.Errorf("the meta of pages 2: %v", res.Meta)
	}
	// By name, whatever the bytes; and by its first bytes, whatever the name.
	pages["named.PDF"], pages["renamed.txt"] = 1, 1
	mustContain(t, f.ok(f.read, in{"file_path": f.file("named.PDF", "not really")}), "[page 1]\nText of page 1.")
	mustContain(t, f.ok(f.read, in{"file_path": f.file("renamed.txt", "%PDF-1.4\nstuff")}), "[page 1]\nText of page 1.")

	long := pdf("long.pdf", 25)
	f.fails(f.read, in{"file_path": long}, "is a PDF of 25 pages, more than the 10 that Read reads whole", `pages "1-20", then "21-25"`)
	mustContain(t, f.ok(f.read, in{"file_path": long, "pages": "21-25"}), "here is the text of pages 21-25.]", "[page 25]\nText of page 25.")
	mustContain(t, f.ok(f.read, in{"file_path": long, "pages": " 20 - 30 "}), "here is the text of pages 20-25.]")
	f.fails(f.read, in{"file_path": long, "pages": "1-21"}, `pages "1-21" is 21 pages`, "at most 20 a call", `"1-20"`)
	f.fails(f.read, in{"file_path": long, "pages": "30"}, "is a PDF of 25 pages, so there is no page 30")
	for _, bad := range []string{"x", "5-2", "0", "-3", "2-"} {
		f.fails(f.read, in{"file_path": long, "pages": bad}, "not a page or a range of pages")
	}
	if slices.Contains(asked, "long.pdf 1-21") || slices.Contains(asked, "long.pdf 1-25") {
		t.Errorf("text was taken out for a call that was refused: %v", asked)
	}

	// Past what one call shows: whole pages, and where to read on.
	mustContain(t, f.ok(f.read, in{"file_path": pdf("big.pdf", 8)}),
		"here is the text of pages 1-3.]", "[page 3]", "[pages 4-8 more than one call shows: read on with pages \"4-8\".]")
	// One page alone past it: its start.
	mustContain(t, f.ok(f.read, in{"file_path": pdf("huge.pdf", 1)}), "[... the rest of this page is more than one call shows]")

	mustContain(t, f.ok(f.read, in{"file_path": pdf("scan.pdf", 2)}), "holds no text that can be taken out", "pdftoppm -png")
	f.fails(f.read, in{"file_path": pdf("broken.pdf", 0)}, "could not be read as a PDF", "Couldn't find trailer dictionary")
	f.fails(f.read, in{"file_path": pdf("none.pdf", 0)}, "poppler", "not installed", "poppler-utils")

	// pages on a file that is not a PDF is still refused.
	f.fails(f.read, in{"file_path": f.file("plain.md", "text\n"), "pages": "1"}, "pages is for PDF files")
}

// With poppler's own tools, where they are installed: a PDF made here of
// two pages is read whole, and by one page.
func TestReadPDFWithPoppler(t *testing.T) {
	for _, tool := range []string{"pdfinfo", "pdftotext"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	f := newFixture(t, Options{})
	p := f.file("two.pdf", string(minimalPDF("Hello from page one", "Second page here")))
	out := f.ok(f.read, in{"file_path": p})
	mustContain(t, out, "is a PDF of 2 pages; here is the text of pages 1-2.]", "[page 1]", "Hello from page one", "[page 2]", "Second page here")
	out = f.ok(f.read, in{"file_path": p, "pages": "2"})
	mustContain(t, out, "[page 2]", "Second page here")
	if strings.Contains(out, "Hello from page one") {
		t.Errorf("page 2 alone holds page 1: %s", out)
	}
	f.fails(f.read, in{"file_path": f.file("bad.pdf", "%PDF-1.4\nnot a PDF at all\n")}, "could not be read as a PDF")
}

// minimalPDF makes a PDF whose pages each hold one line of text.
func minimalPDF(pages ...string) []byte {
	var b bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	b.WriteString("%PDF-1.4\n")
	kids := ""
	for i := range pages {
		kids += fmt.Sprintf("%d 0 R ", 4+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(pages)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	for i, text := range pages {
		stream := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", text)
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return b.Bytes()
}

// The number of pages is pdfinfo's last line of it: a title printed before
// it may hold a line that says otherwise.
func TestPagesFrom(t *testing.T) {
	if n, err := pagesFrom("Title:          a\nPages: 1\nAuthor:         b\nPages:          300\n"); err != nil || n != 300 {
		t.Errorf("pagesFrom = %d, %v; want 300", n, err)
	}
	if _, err := pagesFrom("Title: nothing\n"); err == nil {
		t.Error("no Pages line gave no error")
	}
}
