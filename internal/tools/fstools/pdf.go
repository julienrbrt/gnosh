package fstools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file is Read for PDFs. Claude Code's Read takes a PDF whole when it
// has at most 10 pages, and otherwise only by pages, at most 20 a call (its
// tools reference, "Read tool behavior"; it renders pages with poppler's
// pdftoppm). gnosh's Read gives the PDF's text, page by page, by poppler's
// pdftotext: text every model can read, for far less of its context than
// pictures of the pages. A page with no text in it, as a scan has none, is
// said to be one, with how to make a picture of it to Read.

const (
	// pdfWholeMost is the most pages of a PDF that Read reads whole.
	pdfWholeMost = 10
	// pdfCallMost is the most pages one call reads.
	pdfCallMost = 20
	// pdfToolWait is how long pdfinfo or pdftotext may take.
	pdfToolWait = time.Minute
	// pdfToolMost is the most a poppler tool may print.
	pdfToolMost = 16 << 20
	// readLargeMost is the most text a Read with allow_large returns.
	readLargeMost = 8 << 20
)

// errNoPoppler is the error of a poppler tool that is not installed.
var errNoPoppler = errors.New("not installed")

// popplerPages is how many pages a PDF has, by pdfinfo.
func popplerPages(ctx context.Context, path string) (int, error) {
	out, err := runPoppler(ctx, "pdfinfo", path)
	if err != nil {
		return 0, err
	}
	return pagesFrom(out)
}

// pagesFrom reads the number of pages in what pdfinfo printed.
func pagesFrom(out string) (int, error) {
	// The last line that says so: a title or a subject, which pdfinfo
	// prints before it, can hold a line of its own (the review of the
	// day's code, 2026-11-20).
	pages := ""
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "Pages:"); ok {
			pages = strings.TrimSpace(rest)
		}
	}
	if pages == "" {
		return 0, errors.New("pdfinfo did not say how many pages it has")
	}
	return strconv.Atoi(pages)
}

// popplerText is the text of pages first to last of a PDF, by pdftotext:
// each page ends with a form feed.
func popplerText(ctx context.Context, path string, first, last int) (string, error) {
	return runPoppler(ctx, "pdftotext", "-f", strconv.Itoa(first), "-l", strconv.Itoa(last), "-layout", "-enc", "UTF-8", path, "-")
}

// runPoppler runs one of poppler's tools on the PATH and returns what it
// printed.
func runPoppler(ctx context.Context, name string, args ...string) (string, error) {
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, errNoPoppler)
	}
	ctx, cancel := context.WithTimeout(ctx, pdfToolWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	out, errOut := &cappedBuffer{most: pdfToolMost}, &cappedBuffer{most: 4 << 10, drop: true}
	cmd.Stdout, cmd.Stderr = out, errOut
	err = cmd.Run()
	switch {
	case out.over:
		return "", fmt.Errorf("%s printed more than %s", name, byteSize(pdfToolMost))
	case ctx.Err() != nil:
		return "", fmt.Errorf("%s took longer than %s", name, pdfToolWait)
	case err != nil:
		why := strings.TrimSpace(errOut.b.String())
		if i := strings.IndexByte(why, '\n'); i >= 0 {
			why = why[:i]
		}
		if why == "" {
			why = err.Error()
		}
		return "", fmt.Errorf("%s: %s", name, why)
	}
	return out.b.String(), nil
}

// cappedBuffer keeps what is written to it up to most bytes, and then
// refuses more, which ends the command writing; or, with drop, lets the
// rest go by unkept.
type cappedBuffer struct {
	b    bytes.Buffer
	most int
	drop bool
	over bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.b.Len()+len(p) > c.most {
		c.over = true
		if c.drop {
			c.b.Write(p[:max(c.most-c.b.Len(), 0)])
			return len(p), nil
		}
		return 0, errors.New("too much output")
	}
	return c.b.Write(p)
}

// pageRange reads a Read call's pages ("3", "1-5") against a PDF of total
// pages: without pages, a PDF of at most pdfWholeMost pages is read whole.
func (o *Options) pageRange(spec string, total int, loc *location) (first, last int, f *failure) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		if total > pdfWholeMost {
			return 0, 0, failf("%s is a PDF of %d pages, more than the %d that %s reads whole. Read it by pages, at most %d a call: pages \"1-%d\", then \"%d-%d\", and on.",
				loc.abs, total, pdfWholeMost, o.ReadName, pdfCallMost, pdfCallMost, pdfCallMost+1, min(2*pdfCallMost, total))
		}
		return 1, max(total, 1), nil
	}
	a, b, isRange := strings.Cut(spec, "-")
	first, err := strconv.Atoi(strings.TrimSpace(a))
	last = first
	if err == nil && isRange {
		last, err = strconv.Atoi(strings.TrimSpace(b))
	}
	switch {
	case err != nil || first < 1 || last < first:
		return 0, 0, failf("pages is %q, which is not a page or a range of pages such as \"3\" or \"1-5\". Nothing was read.", spec)
	case first > total:
		return 0, 0, failf("%s is a PDF of %s, so there is no page %d. Nothing was read.", loc.abs, countOf(total, "page", "pages"), first)
	case last-first+1 > pdfCallMost:
		return 0, 0, failf("pages %q is %d pages, and %s reads at most %d a call. Ask for \"%d-%d\", then go on from there.", spec, last-first+1, o.ReadName, pdfCallMost, first, first+pdfCallMost-1)
	}
	return first, min(last, total), nil
}

// readPDF gives a PDF's text, page by page.
func (t *readTool) readPDF(ctx context.Context, loc *location, pages string) (core.ToolResult, error) {
	o := t.opts
	stopped := func() (core.ToolResult, error) {
		return core.ToolResult{}, fmt.Errorf("%s of %s stopped: %w", o.ReadName, loc.abs, context.Cause(ctx))
	}
	pagesOf, textOf := o.PDFPages, o.PDFText
	if pagesOf == nil {
		pagesOf = popplerPages
	}
	if textOf == nil {
		textOf = popplerText
	}
	total, err := pagesOf(ctx, loc.real)
	if err != nil {
		if ctx.Err() != nil {
			return stopped()
		}
		if errors.Is(err, errNoPoppler) {
			return refused(failf("%s is a PDF, and %s reads PDFs with poppler's pdfinfo and pdftotext, which are not installed here (the package poppler-utils, or poppler). Install them, or turn it into text with another shell command and read that.", loc.abs, o.ReadName))
		}
		return refused(failf("%s could not be read as a PDF (%v).", loc.abs, err))
	}
	first, last, f := o.pageRange(pages, total, loc)
	if f != nil {
		return refused(f)
	}
	text, err := textOf(ctx, loc.real, first, last)
	if err != nil {
		if ctx.Err() != nil {
			return stopped()
		}
		if errors.Is(err, errNoPoppler) {
			return refused(failf("%s is a PDF, and %s reads its text with poppler's pdftotext, which is not installed here (the package poppler-utils, or poppler). Install it, or turn the PDF into text with another shell command and read that.", loc.abs, o.ReadName))
		}
		return refused(failf("The text of %s could not be taken out (%v).", loc.abs, err))
	}

	// Each page, under a line that says which it is, its lines cut as a
	// text file's are, until a page would take the text past what one
	// call shows: the rest is for the next call.
	texts := strings.Split(text, "\f")
	var b strings.Builder
	shown, hasText, cut := first-1, false, false
	for i := 0; first+i <= last; i++ {
		page := ""
		if i < len(texts) {
			page = texts[i]
		}
		var pb strings.Builder
		fmt.Fprintf(&pb, "[page %d]\n", first+i)
		for _, line := range strings.Split(strings.TrimRight(page, " \n"), "\n") {
			s, _ := showable([]byte(strings.TrimRight(line, " ")))
			if kept, dropped := cutChars([]byte(s), o.ReadMaxLineChars); dropped > 0 {
				s = string(kept) + cutMark(dropped)
			}
			pb.WriteString(s + "\n")
		}
		if strings.TrimSpace(page) != "" {
			hasText = true
		}
		if b.Len()+pb.Len() > o.ReadMaxBytes {
			if b.Len() == 0 {
				// One page is more than a call shows: its start.
				b.WriteString(cutBytes(pb.String(), o.ReadMaxBytes))
				b.WriteString("\n[... the rest of this page is more than one call shows]\n")
				shown = first + i
			}
			cut = true
			break
		}
		b.WriteString(pb.String())
		shown = first + i
	}
	of := countOf(total, "page", "pages")
	res := core.ToolResult{}
	switch {
	case !hasText:
		res = core.TextResult(fmt.Sprintf("[%s is a PDF of %s, and %s holds no text that can be taken out: it may be pictures of pages, as a scan is. Make a picture of a page with a shell command (pdftoppm -png -r 100 -f %d -l %d <the PDF> page) and %s that.]",
			loc.abs, of, pagesText(first, last), first, first, o.ReadName))
	default:
		head := fmt.Sprintf("[%s is a PDF of %s; here is the text of %s.]\n", loc.abs, of, pagesText(first, shown))
		tail := ""
		if cut && shown < last {
			tail = fmt.Sprintf("[%s more than one call shows: read on with pages \"%d-%d\".]\n", pagesText(shown+1, last), shown+1, last)
		}
		res = core.TextResult(head + b.String() + tail)
	}
	res.Meta = map[string]any{
		"type": "pdf",
		"file": map[string]any{"filePath": loc.abs, "numPages": total, "firstPage": first, "lastPage": shown},
	}
	if loc.real != loc.abs {
		res.Meta["realPath"] = loc.real
	}
	return res, nil
}

// pagesText names pages first to last: "page 3", "pages 1-5".
func pagesText(first, last int) string {
	if first >= last {
		return fmt.Sprintf("page %d", first)
	}
	return fmt.Sprintf("pages %d-%d", first, last)
}

// cutBytes cuts s to at most n bytes, at the start of a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
