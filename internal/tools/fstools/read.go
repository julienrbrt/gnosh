package fstools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// readTool is the Read tool.
type readTool struct {
	opts *Options
}

func (t *readTool) props() []prop {
	return []prop{
		{name: "file_path", kind: "string", desc: "Path of the file to read. Absolute is best; a relative path is taken from the working directory."},
		{name: "offset", kind: "integer", desc: "Line number to start at; the first line is 1. Leave out to start at the top."},
		{name: "limit", kind: "integer", desc: fmt.Sprintf("How many lines to read. Leave out for the first %d.", t.opts.ReadDefaultLines)},
		{name: "pages", kind: "string", desc: fmt.Sprintf("For a PDF: the pages to read, such as \"3\" or \"1-5\", at most %d a call. Needed for a PDF of more than %d pages.", pdfCallMost, pdfWholeMost)},
		{name: "allow_large", kind: "boolean", desc: "Read the whole file, or a long part of it, in one call, past the usual limits, as far as the context has room for. Only when all of it is needed."},
	}
}

// Spec tells the model what Read does.
func (t *readTool) Spec() core.ToolSpec {
	o := t.opts
	desc := fmt.Sprintf(`Reads a file and returns its text with line numbers.

Each line comes back as its number, a tab, then the line. The number and the tab are not in the file: leave them out of anything you pass to %[1]s.

One call shows at most %[2]d lines (or the limit you give) and at most %[3]d bytes of text. When the file has more, a note at the end says so and gives the offset to continue from. A line longer than %[4]d characters is cut and marked. For a large file, read the part you need with offset and limit, or find it first with %[5]s.

PNG, JPEG, GIF and WebP images come back as pictures, a large one shrunk to fit what a model is shown. A PDF comes back as its text, page by page: one of more than %[7]d pages is read with pages, at most %[8]d at a time. A Jupyter notebook (.ipynb) comes back as its cells, each with its id, and their outputs; change a cell with %[9]s. A directory or a binary file is not read: the result says what it is. An empty file and an offset past the end give a short notice, not an error.

%[1]s and %[6]s only change what this session has been shown, so read a file before changing it.`,
		o.EditName, o.ReadDefaultLines, o.ReadMaxBytes, o.ReadMaxLineChars, o.GrepName, o.WriteName, pdfWholeMost, pdfCallMost, o.NotebookEditName)
	return core.ToolSpec{
		Name:        o.ReadName,
		Description: desc,
		InputSchema: schema([]string{"file_path"}, t.props()...),
	}
}

// readArgs is a Read call's arguments, checked.
type readArgs struct {
	path   string
	offset int // first line wanted, from 1
	limit  int // lines wanted
	// ranged says the call asked for a part of the file by offset or limit.
	ranged bool
	// pages is the PDF page range (pdf.go).
	pages string
	// allowLarge is the request to read past the usual limits, as far as
	// the context has room for (Options.ReadRoom); limitGiven says the
	// call set limit.
	allowLarge, limitGiven bool
}

func (t *readTool) parse(call core.ToolCall) (readArgs, *failure) {
	var r readArgs
	names := propNames(t.props())
	a, f := parseArgs(t.opts.ReadName, call.Input, names...)
	if f != nil {
		return r, f
	}
	if r.path, f = a.required("file_path"); f != nil {
		return r, f
	}
	offset, offsetGiven, f := a.whole("offset")
	if f != nil {
		return r, f
	}
	limit, limitGiven, f := a.whole("limit")
	if f != nil {
		return r, f
	}
	r.offset = max(offset, 1)
	r.limit = limit
	r.limitGiven = limitGiven && limit > 0
	if !r.limitGiven {
		r.limit = t.opts.ReadDefaultLines
	}
	r.ranged = (offsetGiven && offset > 1) || (limitGiven && limit > 0)
	if r.pages, _, f = a.text("pages"); f != nil {
		return r, f
	}
	if r.allowLarge, _, f = a.flag("allow_large"); f != nil {
		return r, f
	}
	return r, nil
}

// Describe says which file a Read call would read. It does not say
// whether the file is there: that is for the call itself to find out,
// after permission is settled.
func (t *readTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	args, f := t.parse(call)
	if f != nil {
		return core.Action{}, f
	}
	loc, f := t.opts.locateFile(env, t.opts.ReadName, args.path)
	if f != nil {
		return core.Action{}, f
	}
	summary := "Read " + loc.abs
	if args.pages != "" {
		summary += ", pages " + args.pages
	} else if args.ranged {
		summary += fmt.Sprintf(", %s from line %d", countOf(args.limit, "line", "lines"), args.offset)
	}
	return core.Action{Kind: core.ActionRead, Paths: loc.paths(), Summary: summary}, nil
}

// Run reads the file.
func (t *readTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	o := t.opts
	args, f := t.parse(call)
	if f != nil {
		return refused(f)
	}
	loc, f := o.locateFile(env, o.ReadName, args.path)
	if f != nil {
		return refused(f)
	}
	if loc.trouble != nil {
		return refused(loc.trouble)
	}
	if !loc.exists {
		return refused(missingFile(&loc))
	}
	if loc.info.IsDir() {
		return refused(failf("%s is a directory, not a file. To see what is in it, use %s with a pattern such as \"*\", or a shell command.", loc.abs, o.GlobName))
	}
	if !loc.info.Mode().IsRegular() {
		return refused(failf("%s is %s, not a file with contents to read.", loc.abs, kindOf(loc.info.Mode())))
	}
	file, info, f := openRegular(loc.real)
	if f != nil {
		return refused(f)
	}
	defer file.Close()

	buf := make([]byte, 64<<10)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return refused(failf("%s could not be read: %v.", loc.abs, underlying(err)))
	}
	atEnd := err != nil
	head := buf[:n]
	ext := strings.ToLower(filepath.Ext(loc.real))

	switch {
	case isPDF(head) || ext == ".pdf":
		return t.readPDF(ctx, &loc, args.pages)
	case args.pages != "":
		return refused(failf("%s: pages is for PDF files, and %s is not one. Use offset and limit to read part of a text file.", o.ReadName, loc.abs))
	case ext == ".ipynb":
		return t.readNotebook(ctx, env, &loc, file, info, head, atEnd)
	}
	if media := imageType(head); media != "" {
		return t.readImage(ctx, env, &loc, file, info, head, atEnd, media)
	}
	if n == 0 {
		sum := sha256.Sum256(nil)
		sessionOf(env).saw(loc.real, sighting{info: info, sum: &sum, lines: 0, whole: true})
		res := core.TextResult(fmt.Sprintf("[%s exists but is empty.]", loc.abs))
		res.Meta = readMeta(&loc, 0, 1, 0, false)
		return res, nil
	}
	if looksBinary(head) {
		// The model has now been told all there is to tell about the file,
		// so it may replace it with Write; Edit refuses it on its own.
		sessionOf(env).saw(loc.real, sighting{info: info, lines: -1, whole: true})
		hint := "Look at it with a shell command suited to what it is (file, xxd, strings)."
		if utf16Mark(head) {
			hint = "It starts with a UTF-16 byte order mark, so it is probably text in UTF-16: convert it with a shell command (iconv -f UTF-16 -t UTF-8) and read the result."
		}
		return refused(failf("%s looks like a binary file (%s) and is not shown as text. %s", loc.abs, byteSize(info.Size()), hint))
	}

	// allow_large: the whole file, or as much as the context has room for,
	// past the usual limits.
	maxBytes, large, used := o.ReadMaxBytes, false, 0
	if args.allowLarge && o.ReadRoom != nil {
		room, done := o.ReadRoom(ctx)
		defer func() { done(used) }()
		if room = min(room, readLargeMost); room > maxBytes {
			maxBytes, large = room, true
			if !args.limitGiven {
				args.limit = math.MaxInt32
			}
		}
	}
	tr := &textRead{
		first:    args.offset,
		limit:    args.limit,
		maxBytes: maxBytes,
		maxChars: o.ReadMaxLineChars,
		sum:      sha256.New(),
	}
	if o.ReadResultMost != nil {
		tr.maxOut = o.ReadResultMost(ctx)
	}
	tr.sum.Write(head)
	if bytes.HasPrefix(head, []byte(utf8BOM)) {
		tr.bom = true
		head = head[len(utf8BOM):]
	}
	tr.feed(head)
	for !atEnd {
		if tr.full {
			// The page is complete. Go on only to count the lines, and
			// only so far.
			if o.ReadCountBytes < 0 || tr.past > o.ReadCountBytes {
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, fmt.Errorf("%s of %s stopped: %w", o.ReadName, loc.abs, context.Cause(ctx))
		}
		n, err := io.ReadFull(file, buf)
		chunk := buf[:n]
		tr.sum.Write(chunk)
		if tr.full {
			tr.past += int64(n)
		}
		tr.feed(chunk)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			atEnd = true
		} else if err != nil {
			return refused(failf("%s could not be read to its end: %v.", loc.abs, underlying(err)))
		}
	}
	if atEnd {
		tr.end()
		if tr.lineNo == 0 {
			// Nothing but a byte order mark.
			var sum [sha256.Size]byte
			tr.sum.Sum(sum[:0])
			sessionOf(env).saw(loc.real, sighting{info: info, sum: &sum, lines: 0, whole: true})
			res := core.TextResult(fmt.Sprintf("[%s exists but has no text in it: only a byte order mark.]", loc.abs))
			res.Meta = readMeta(&loc, 0, 1, 0, false)
			return res, nil
		}
	}

	// What the session has now seen of the file.
	v := sighting{info: info, lines: -1}
	if atEnd {
		v.lines = tr.lineNo
		var sum [sha256.Size]byte
		tr.sum.Sum(sum[:0])
		v.sum = &sum
	}
	if tr.shown > 0 {
		v.shown = span{tr.firstShown, tr.lastShown}
	}
	sessionOf(env).saw(loc.real, v)

	total := -1
	if atEnd {
		total = tr.lineNo
	}
	more := !atEnd || tr.lastShown < tr.lineNo
	used = tr.out.Len()
	var out strings.Builder
	out.WriteString(tr.out.String())
	var notes []string
	switch {
	case tr.shown == 0 && atEnd:
		notes = append(notes, fmt.Sprintf("offset %d is past the end of %s: the file has %s.", args.offset, loc.abs, countOf(tr.lineNo, "line", "lines")))
	case tr.shown == 0:
		// Not reached: the loop above reads on until the page has a line
		// or the file ends.
		notes = append(notes, "No lines were read.")
	case more:
		note := fmt.Sprintf("Showing lines %d-%d", tr.firstShown, tr.lastShown)
		if total >= 0 {
			note += fmt.Sprintf(" of %d.", total)
		} else {
			note += fmt.Sprintf("; the file goes on (it is %s, and its lines were not all counted).", byteSize(info.Size()))
		}
		switch {
		case tr.stoppedByOut:
			note += fmt.Sprintf(" This call stopped at the %s one result may hold for this model.", byteSize(int64(tr.maxOut)))
		case tr.stoppedByBytes && large:
			note += fmt.Sprintf(" This call stopped at the %s the context has room for now, with allow_large.", byteSize(int64(maxBytes)))
		case tr.stoppedByBytes:
			note += fmt.Sprintf(" This call stopped at the %d bytes one call may show.", o.ReadMaxBytes)
		}
		note += fmt.Sprintf(" To read on, call %s again with offset=%d.", o.ReadName, tr.lastShown+1)
		if args.allowLarge && !large {
			note += " (allow_large finds no more room than that here.)"
		}
		notes = append(notes, note)
	case tr.firstShown > 1:
		notes = append(notes, fmt.Sprintf("Showing lines %d-%d of %d: this is the end of the file.", tr.firstShown, tr.lastShown, total))
	}
	if tr.shown > 0 {
		switch {
		case tr.crlf > 0 && tr.lf == 0:
			notes = append(notes, fmt.Sprintf("Lines end with CRLF; the carriage returns are left out here. %s matches line breaks either way and keeps them as they are.", o.EditName))
		case tr.crlf > 0:
			notes = append(notes, fmt.Sprintf("Line endings are mixed: some lines end with CRLF, some with LF. The carriage returns are left out here; %s keeps each line's ending.", o.EditName))
		}
		if tr.bom && tr.firstShown == 1 {
			notes = append(notes, fmt.Sprintf("The file starts with a UTF-8 byte order mark, left out here; %s keeps it.", o.EditName))
		}
		if tr.bad > 0 {
			notes = append(notes, fmt.Sprintf("Bytes that are not text (not valid UTF-8, or NUL) are shown as U+FFFD here, in %s. %s does not change a file that is not valid UTF-8: use a shell command that keeps its encoding.", countOf(tr.bad, "place", "places"), o.EditName))
		}
	}
	if len(notes) > 0 {
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		for _, note := range notes {
			out.WriteString("[" + note + "]\n")
		}
	}
	res := core.TextResult(out.String())
	start := tr.firstShown
	if tr.shown == 0 {
		start = args.offset
	}
	res.Meta = readMeta(&loc, tr.shown, start, total, more && tr.shown > 0)
	return res, nil
}

// readMeta is the structured result of a text read, with the key names
// Claude Code documents for it. The text itself is left out: it is in the
// result's content.
func readMeta(loc *location, shown, start, total int, more bool) map[string]any {
	file := map[string]any{
		"filePath":  loc.abs,
		"numLines":  shown,
		"startLine": start,
	}
	if total >= 0 {
		file["totalLines"] = total
	}
	meta := map[string]any{"type": "text", "file": file}
	if more {
		meta["truncated"] = true
	}
	if loc.real != loc.abs {
		meta["realPath"] = loc.real
	}
	return meta
}

// readImage returns an image file as a picture.
func (t *readTool) readImage(ctx context.Context, env *core.ToolEnv, loc *location, file *os.File, info os.FileInfo, head []byte, atEnd bool, media string) (core.ToolResult, error) {
	o := t.opts
	tooBig := func(why string) (core.ToolResult, error) {
		return refused(failf("%s is an image of %s, more than the %s that %s hands to a model, and %s. Make a smaller copy with an image tool and read that.",
			loc.abs, byteSize(max(info.Size(), int64(len(head)))), byteSize(o.ReadMaxImageBytes), o.ReadName, why))
	}
	readMost := max(o.ReadMaxImageBytes, imageReadMost)
	if info.Size() > readMost {
		return tooBig("it is too large to shrink here")
	}
	data := append([]byte(nil), head...)
	if !atEnd {
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, fmt.Errorf("%s of %s stopped: %w", o.ReadName, loc.abs, context.Cause(ctx))
		}
		rest, err := io.ReadAll(io.LimitReader(file, readMost-int64(len(data))+1))
		if err != nil {
			return refused(failf("%s could not be read: %v.", loc.abs, underlying(err)))
		}
		data = append(data, rest...)
	}
	if int64(len(data)) > readMost {
		return tooBig("it is too large to shrink here")
	}
	sum := sha256.Sum256(data)
	kind := strings.ToUpper(strings.TrimPrefix(media, "image/"))
	said := fmt.Sprintf("[%s: a %s image, %s.]", loc.abs, kind, byteSize(int64(len(data))))
	sent, sentMedia := data, media
	// Larger than a model takes, or than a model is shown: shrunk
	// (shrink.go), as Claude Code shrinks it.
	small, shrank, err := shrinkImage(ctx, data, media, o.ReadMaxImageBytes)
	switch {
	case shrank:
		sent, sentMedia = small.data, small.media
		how := "shrunk to"
		switch {
		case small.turned && small.width == small.fromW && small.height == small.fromH:
			how = "turned upright, as its EXIF orientation says, at"
		case small.turned:
			how = "turned upright, as its EXIF orientation says, and shrunk to"
		}
		said = fmt.Sprintf("[%s: a %s image, %s, %dx%d, shown %s %dx%d (%s, %s).]", loc.abs, kind, byteSize(int64(len(data))),
			small.fromW, small.fromH, how, small.width, small.height, strings.ToUpper(strings.TrimPrefix(small.media, "image/")), byteSize(int64(len(small.data))))
		if media == "image/gif" {
			said = strings.TrimSuffix(said, ".]") + "; of a GIF, the first frame only.]"
		}
	case int64(len(data)) > o.ReadMaxImageBytes && err != nil:
		return tooBig("it could not be shrunk: " + err.Error())
	case int64(len(data)) > o.ReadMaxImageBytes:
		return tooBig(o.ReadName + " does not shrink a " + kind + " image")
	}
	sessionOf(env).saw(loc.real, sighting{info: info, sum: &sum, lines: -1, whole: true})
	res := core.ToolResult{Content: []core.Block{
		core.Text(said),
		{Type: core.BlockImage, MediaType: sentMedia, Data: sent},
	}}
	about := map[string]any{"filePath": loc.abs, "type": sentMedia, "originalSize": len(data)}
	if shrank {
		about["dimensions"] = map[string]any{"originalWidth": small.fromW, "originalHeight": small.fromH, "displayWidth": small.width, "displayHeight": small.height}
	}
	res.Meta = map[string]any{"type": "image", "file": about}
	if loc.real != loc.abs {
		res.Meta["realPath"] = loc.real
	}
	return res, nil
}

// missingFile says that a file is not there, and where the path stops
// existing when that is above the file itself.
func missingFile(loc *location) *failure {
	dir := filepath.Dir(loc.real)
	for dir != filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	link := ""
	if isLink(loc.abs) {
		link = fmt.Sprintf(" %s is a symbolic link to %s, which is not there.", loc.abs, loc.real)
	}
	if dir != filepath.Dir(loc.real) {
		return failf("%s does not exist: there is no directory %s (the path exists as far as %s).%s", loc.abs, filepath.Dir(loc.real), dir, link)
	}
	if near := similarNames(loc.real); len(near) > 0 {
		return failf("%s does not exist.%s In that directory there is: %s.", loc.abs, link, strings.Join(near, ", "))
	}
	return failf("%s does not exist.%s", loc.abs, link)
}

// similarNames looks in a missing file's directory for names the model
// may have meant: the same name in another letter case, or the same name
// with another extension.
func similarNames(path string) []string {
	dir, base := filepath.Split(path)
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	// A directory too big to look through quickly gets no suggestions.
	const most = 5000
	entries, err := d.ReadDir(most + 1)
	if (err != nil && err != io.EOF) || len(entries) > most {
		return nil
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	var out []string
	for _, e := range entries {
		name := e.Name()
		sameStem := stem != "" && strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), stem)
		if strings.EqualFold(name, base) || sameStem {
			out = append(out, name)
			if len(out) == 5 {
				break
			}
		}
	}
	return out
}

// textRead turns a file's bytes, fed to it in pieces, into the page of
// numbered lines a Read call shows, and counts the lines it passes.
type textRead struct {
	// What the call asked for.
	first    int // first line to show
	limit    int // most lines to show
	maxBytes int // most bytes of text to show
	maxChars int // most characters of one line to show
	maxOut   int // most bytes of the page, line numbers and all (0: none)

	// The page.
	out            strings.Builder
	shown          int
	firstShown     int
	lastShown      int
	shownBytes     int
	full           bool // the page takes no more lines
	stoppedByBytes bool // ... because of maxBytes
	stoppedByOut   bool // ... because of maxOut
	crlf, lf       int  // line endings among the lines shown
	bom            bool // the file starts with a byte order mark
	bad            int  // stretches of bytes shown as U+FFFD

	// The file as a whole.
	lineNo int       // lines ended so far
	past   int64     // bytes read since the page filled
	sum    hash.Hash // of every byte fed

	// The line being put together.
	cur      []byte // its first bytes, as many as could be shown
	curLen   int64  // its length so far in bytes
	over     int    // characters of it beyond cur
	lastByte byte
}

// wanted reports whether the line now being put together belongs on the
// page.
func (r *textRead) wanted() bool {
	return !r.full && r.lineNo+1 >= r.first
}

// feed takes the next piece of the file.
func (r *textRead) feed(chunk []byte) {
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			r.add(chunk)
			return
		}
		r.add(chunk[:i])
		r.endLine(true)
		chunk = chunk[i+1:]
	}
}

// add appends bytes to the line being put together.
func (r *textRead) add(b []byte) {
	if len(b) == 0 {
		return
	}
	r.curLen += int64(len(b))
	r.lastByte = b[len(b)-1]
	if !r.wanted() {
		return
	}
	// Keep enough bytes that maxChars characters always fit.
	room := r.maxChars*utf8.UTFMax + utf8.UTFMax - len(r.cur)
	if room >= len(b) {
		r.cur = append(r.cur, b...)
		return
	}
	if room > 0 {
		// Stop at the start of a character, so that none is counted in
		// both halves.
		for room > 0 && !isRuneStart(b[room]) {
			room--
		}
		r.cur = append(r.cur, b[:room]...)
		b = b[room:]
	}
	r.over += runeStarts(b)
}

// end is called when the file has ended: a last line with no line break
// after it is a line all the same.
func (r *textRead) end() {
	if r.curLen > 0 {
		r.endLine(false)
	}
}

// endLine finishes the line being put together.
func (r *textRead) endLine(terminated bool) {
	wanted := r.wanted()
	r.lineNo++
	if wanted {
		line := r.cur
		whole := int64(len(line)) == r.curLen
		crlf := terminated && r.lastByte == '\r'
		if crlf && whole {
			line = line[:len(line)-1]
		}
		kept, dropped := cutChars(line, r.maxChars)
		dropped += r.over
		if dropped == 0 && !whole {
			// Bytes were left out that begin no character: the line is cut
			// all the same, and must say so.
			dropped = 1
		}
		text, bad := showable(kept)
		if dropped > 0 {
			text += cutMark(dropped)
		}
		cost := len(text) + 1
		num := strconv.Itoa(r.lineNo)
		switch {
		case r.shown > 0 && r.shownBytes+cost > r.maxBytes:
			r.full = true
			r.stoppedByBytes = true
		case r.shown > 0 && r.maxOut > 0 && r.out.Len()+len(num)+1+cost > r.maxOut:
			r.full = true
			r.stoppedByOut = true
		default:
			r.out.WriteString(num)
			r.out.WriteByte('\t')
			r.out.WriteString(text)
			r.out.WriteByte('\n')
			if r.shown == 0 {
				r.firstShown = r.lineNo
			}
			r.shown++
			r.lastShown = r.lineNo
			r.shownBytes += cost
			r.bad += bad
			if crlf {
				r.crlf++
			} else if terminated {
				r.lf++
			}
			if r.shown >= r.limit {
				r.full = true
			}
		}
	}
	r.cur = r.cur[:0]
	r.curLen = 0
	r.over = 0
	r.lastByte = 0
}
