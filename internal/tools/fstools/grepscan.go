package fstools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// grepMode is what a Grep call wants back.
type grepMode int

const (
	modeFiles   grepMode = iota // the files that have a match
	modeContent                 // the matching lines
	modeCount                   // how many lines match in each file
)

// scanSpec is what to look for in a file and how much to tell about it.
// It is the part of a Grep call that does not depend on which file is
// being searched.
type scanSpec struct {
	re        *regexp.Regexp
	mode      grepMode
	multiline bool
	// only asks for the matched parts alone, not their whole lines.
	only bool
	// before and after are how many lines of context to give around each
	// matching line.
	before, after int

	// searchBinary lets files that look binary be searched.
	searchBinary bool
	// maxLine is the longest line matched in one piece.
	maxLine int
	// maxColumns is the most bytes of a line that is shown.
	maxColumns int
	// maxWhole is the largest file read whole for a multiline search.
	maxWhole int64

	// literal, when not empty, is text that every match of re begins with.
	// A line without it cannot match, nor can a file, so both are passed
	// over without the pattern being tried on them. fold is the same for
	// a pattern that ignores letter case: lower-case ASCII text that every
	// match holds in one letter case or another. Both are only ever a
	// short cut: leaving them empty changes no answer.
	literal []byte
	fold    []byte
	// bufs keeps read buffers from one file to the next, so that a search
	// of many files does not make a new one for each.
	bufs sync.Pool
}

// grepLine is one line of a content result.
type grepLine struct {
	// num is the line's number in its file.
	num int
	// text is the line, made safe and cut short enough to show.
	text string
	// match says the line holds a match; otherwise it is context.
	match bool
}

// fileScan is what searching one file found.
type fileScan struct {
	// matched says the file has a match.
	matched bool
	// count is how many lines match; in a multiline search, how many
	// matches there are.
	count int
	// entries is how many lines a content result holds for the file.
	entries int
	// binary says the file looked binary and was passed over.
	binary bool
	// tooBig says the file was too large to search whole.
	tooBig bool
	// modTime is when the file was last changed.
	modTime time.Time
}

// scan searches one file. emit, when not nil, is handed every line a
// content result holds for the file, in order, until it returns false.
// With a nil emit the lines are only counted.
func (s *scanSpec) scan(ctx context.Context, path string, emit func(grepLine) bool) (fileScan, error) {
	f, info, fail := openRegular(path)
	if fail != nil {
		return fileScan{}, fail
	}
	defer f.Close()
	res := fileScan{modTime: info.ModTime()}
	r, decodeErr := fromUTF16(f, s.maxWhole)
	if decodeErr != nil {
		if errors.Is(decodeErr, errUTF16TooBig) {
			res.tooBig = true
			return res, nil
		}
		return res, decodeErr
	}
	var err error
	if s.multiline {
		err = s.scanWhole(ctx, r, &res, emit)
	} else {
		err = s.scanLines(ctx, r, &res, emit)
	}
	return res, err
}

// errUTF16TooBig says a UTF-16 file is larger than is decoded to search.
var errUTF16TooBig = errors.New("too large to decode")

// fromUTF16 is the file's text as UTF-8 when it begins with a UTF-16 byte
// order mark, as ripgrep reads such a file (its --encoding auto), and the
// file as it is otherwise: a UTF-16 file was passed over as binary. One of
// more than most bytes is not decoded. (The mark is looked at where it is,
// so that the file is read from its start either way.)
func fromUTF16(f *os.File, most int64) (io.Reader, error) {
	var mark [2]byte
	if n, _ := f.ReadAt(mark[:], 0); n < 2 || !utf16Mark(mark[:]) {
		return f, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, most+3))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > most+2 {
		return nil, errUTF16TooBig
	}
	data = data[2:]
	units := make([]uint16, len(data)/2)
	for i := range units {
		if mark[0] == 0xFF {
			units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
		} else {
			units[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
		}
	}
	return strings.NewReader(string(utf16.Decode(units))), nil
}

// hasShortCut reports whether there is text to look for before trying the
// pattern.
func (s *scanSpec) hasShortCut() bool {
	return len(s.literal) > 0 || len(s.fold) > 0
}

// mayMatch reports whether b could hold a match: it is false only when b
// lacks the text every match must hold.
func (s *scanSpec) mayMatch(b []byte) bool {
	switch {
	case len(s.literal) > 0:
		return bytes.Contains(b, s.literal)
	case len(s.fold) > 0:
		return containsFold(b, s.fold)
	}
	return true
}

// foldLiteral works out, for a pattern that is to ignore letter case,
// text that every match must hold in some letter case. flags are the
// pattern's other flags. It compiles the pattern as it would be without
// ignoring case and takes the literal text every such match begins with:
// a match that ignores case begins with the same characters, each in any
// of its case forms. Of that text it keeps the longest stretch of
// characters whose case forms are all ASCII, so that looking for it with
// ASCII letters folded finds every form. (The letters k and s are not
// such characters: the Kelvin sign and the long s are forms of them.) It
// returns nil when there is no such text worth looking for.
func foldLiteral(pattern, flags string) []byte {
	expr := pattern
	if flags != "" {
		expr = "(?" + flags + ")" + pattern
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	prefix, _ := re.LiteralPrefix()
	var best, cur []byte
	for _, r := range prefix {
		if !foldsWithinASCII(r) {
			cur = nil
			continue
		}
		cur = append(cur, byte(unicode.ToLower(r)))
		if len(cur) > len(best) {
			best = cur
		}
	}
	if len(best) < 2 {
		return nil
	}
	return best
}

// foldsWithinASCII reports whether r and every other case form of it,
// by the tables the regexp package folds case with, are ASCII.
func foldsWithinASCII(r rune) bool {
	if r >= utf8.RuneSelf {
		return false
	}
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// containsFold reports whether b holds q in any mix of ASCII letter
// cases. q must be lower-case ASCII.
func containsFold(b, q []byte) bool {
	if len(q) == 0 {
		return true
	}
	lower, upper := q[0], q[0]
	if lower >= 'a' && lower <= 'z' {
		upper = lower - ('a' - 'A')
	}
	// find returns where c next is at or after from, or len(b).
	find := func(c byte, from int) int {
		if from < len(b) {
			if j := bytes.IndexByte(b[from:], c); j >= 0 {
				return from + j
			}
		}
		return len(b)
	}
	// Each of the two is looked for from where it was last found, so b is
	// gone over twice at most however often q's first letter comes.
	nextLower, nextUpper := find(lower, 0), len(b)
	if upper != lower {
		nextUpper = find(upper, 0)
	}
	for {
		i := min(nextLower, nextUpper)
		if i+len(q) > len(b) {
			return false
		}
		same := true
		for j := 1; j < len(q) && same; j++ {
			c := b[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			same = c == q[j]
		}
		if same {
			return true
		}
		if i == nextLower {
			nextLower = find(lower, i+1)
		} else {
			nextUpper = find(upper, i+1)
		}
	}
}

// contextLines is the lines kept in hand in case the next match wants
// them shown before it: the last few that were neither matches nor shown
// as context already. It grows only as far as it is filled, so a search
// that asks for much context pays for it only in files long enough to
// use it.
type contextLines struct {
	// most is how many lines to keep.
	most  int
	lines []grepLine
	// next is where the next line goes once lines is full; it is also
	// where the oldest line is.
	next int
}

func (c *contextLines) push(l grepLine) {
	switch {
	case c.most <= 0:
	case len(c.lines) < c.most:
		c.lines = append(c.lines, l)
	default:
		c.lines[c.next] = l
		c.next = (c.next + 1) % c.most
	}
}

// drain calls fn for the held lines, oldest first, until it returns
// false, and lets them all go. It reports whether fn always returned
// true.
func (c *contextLines) drain(fn func(grepLine) bool) bool {
	ok := true
	for i := 0; i < len(c.lines) && ok; i++ {
		ok = fn(c.lines[(c.next+i)%len(c.lines)])
	}
	c.lines = c.lines[:0]
	c.next = 0
	return ok
}

// scanLines searches a file a line at a time.
func (s *scanSpec) scanLines(ctx context.Context, r io.Reader, res *fileScan, emit func(grepLine) bool) error {
	lr := &lineReader{r: r, max: s.maxLine}
	if b, ok := s.bufs.Get().(*[]byte); ok {
		lr.buf = *b
	}
	// Nothing handed out by lr outlives this call: every line is copied
	// before it is kept.
	defer func() { s.bufs.Put(&lr.buf) }()
	head := lr.peek()
	if !s.searchBinary && looksBinary(head) {
		res.binary = true
		return nil
	}
	if bytes.HasPrefix(head, []byte(utf8BOM)) {
		lr.pos += len(utf8BOM)
	}
	if s.hasShortCut() {
		// A file small enough to be in hand whole, with the literal
		// nowhere in it, has no line that can match.
		for !lr.eof && lr.end < len(lr.buf) && lr.fill() {
		}
		if lr.eof && !s.mayMatch(lr.buf[lr.pos:lr.end]) {
			return lr.err
		}
	}
	content := s.mode == modeContent
	withContext := content && !s.only && (s.before > 0 || s.after > 0)
	var held contextLines
	if withContext {
		held.most = s.before
	}
	give := func(l grepLine) bool {
		res.entries++
		return emit == nil || emit(l)
	}
	// lastHit is the line last counted as a match: a line longer than one
	// piece is read in pieces, and a match in each would count it, and
	// show it, again (the search-tools review of 2026-11-20).
	lineNo, fresh, afterLeft, lastHit := 0, true, 0, 0
	for n := 0; ; n++ {
		line, terminated, ok := lr.next()
		if !ok {
			break
		}
		if n&1023 == 1023 {
			if err := ctx.Err(); err != nil {
				return context.Cause(ctx)
			}
		}
		if fresh {
			lineNo++
		}
		// A piece of an over-long line is not the start of a new one.
		fresh = terminated
		if terminated && len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		// A line without the literal cannot match: no need to ask.
		possible := s.mayMatch(line)
		if content && s.only {
			if !possible {
				continue
			}
			all := s.re.FindAllIndex(line, -1)
			if len(all) == 0 {
				continue
			}
			res.matched = true
			if lastHit != lineNo {
				res.count++
				lastHit = lineNo
			}
			for _, m := range all {
				if m[1] == m[0] {
					continue
				}
				l := grepLine{num: lineNo, match: true}
				if emit != nil {
					l.text = s.show(line[m[0]:m[1]], nil)
				}
				if !give(l) {
					return nil
				}
			}
			continue
		}

		var loc []int
		if possible {
			loc = s.re.FindIndex(line)
		}
		if loc == nil {
			if !withContext {
				continue
			}
			l := grepLine{num: lineNo}
			if emit != nil {
				l.text = s.show(line, nil)
			}
			if afterLeft > 0 {
				afterLeft--
				if !give(l) {
					return nil
				}
			} else {
				held.push(l)
			}
			continue
		}
		if lastHit == lineNo {
			// Another piece of a line already counted and shown.
			continue
		}
		lastHit = lineNo
		res.matched = true
		res.count++
		switch s.mode {
		case modeFiles:
			// One match is all this mode needs to know.
			return nil
		case modeCount:
			continue
		}
		if !held.drain(give) {
			return nil
		}
		l := grepLine{num: lineNo, match: true}
		if emit != nil {
			l.text = s.show(line, loc)
		}
		if !give(l) {
			return nil
		}
		afterLeft = s.after
	}
	return lr.err
}

// scanWhole searches a file held whole in memory, so that a match can
// run over line breaks.
func (s *scanSpec) scanWhole(ctx context.Context, r io.Reader, res *fileScan, emit func(grepLine) bool) error {
	data, err := io.ReadAll(io.LimitReader(r, s.maxWhole+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > s.maxWhole {
		res.tooBig = true
		return nil
	}
	if !s.searchBinary && looksBinary(data) {
		res.binary = true
		return nil
	}
	data = bytes.TrimPrefix(data, []byte(utf8BOM))
	if len(data) == 0 {
		// No lines, so nothing to match: as in a search line by line.
		return nil
	}
	if bytes.Contains(data, []byte("\r\n")) {
		// So that a line break in the pattern meets the file's.
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	}
	if !s.mayMatch(data) {
		return nil
	}
	if s.mode == modeFiles {
		if m := s.re.FindAllIndex(data, 2); len(m) > 0 && !(len(m) == 1 && emptyAtEnd(m[0], data)) {
			res.matched, res.count = true, 1
		}
		return nil
	}
	matches := s.re.FindAllIndex(data, -1)
	if n := len(matches); n > 0 && emptyAtEnd(matches[n-1], data) {
		matches = matches[:n-1]
	}
	if len(matches) == 0 {
		return nil
	}
	res.matched, res.count = true, len(matches)
	if s.mode == modeCount {
		return nil
	}

	// starts[i] is where line i+1 begins.
	starts := []int{0}
	for i := 0; i < len(data); {
		j := bytes.IndexByte(data[i:], '\n')
		if j < 0 {
			break
		}
		i += j + 1
		starts = append(starts, i)
	}
	total := len(starts)
	if starts[total-1] == len(data) && total > 1 {
		// What follows the last line break is not a line.
		total--
	}
	lineOf := func(off int) int {
		return sort.Search(len(starts), func(i int) bool { return starts[i] > off })
	}
	lineText := func(n int) []byte {
		end := len(data)
		if n < len(starts) {
			end = starts[n] - 1
		}
		return data[starts[n-1]:end]
	}
	give := func(num int, text []byte, match bool) bool {
		res.entries++
		if emit == nil {
			return true
		}
		return emit(grepLine{num: num, text: s.show(text, nil), match: match})
	}

	if s.only {
		for i, m := range matches {
			if i&1023 == 1023 {
				if err := ctx.Err(); err != nil {
					return context.Cause(ctx)
				}
			}
			if m[1] == m[0] {
				continue
			}
			num := lineOf(m[0])
			part := data[m[0]:m[1]]
			for len(part) > 0 {
				piece := part
				if j := bytes.IndexByte(part, '\n'); j >= 0 {
					piece, part = part[:j], part[j+1:]
				} else {
					part = nil
				}
				if len(piece) > 0 && !give(num, piece, true) {
					return nil
				}
				num++
			}
		}
		return nil
	}

	// Turn the matches into runs of matching lines, then give each run
	// with its context, never the same line twice.
	shown := 0 // the last line given
	for i := 0; i < len(matches); {
		if i&1023 == 1023 {
			if err := ctx.Err(); err != nil {
				return context.Cause(ctx)
			}
		}
		first := lineOf(matches[i][0])
		last := lineOf(max(matches[i][0], matches[i][1]-1))
		i++
		for i < len(matches) && lineOf(matches[i][0]) <= last+1 {
			last = max(last, lineOf(max(matches[i][0], matches[i][1]-1)))
			i++
		}
		first, last = min(first, total), min(last, total)
		for n := max(first-s.before, shown+1); n < first; n++ {
			if !give(n, lineText(n), false) {
				return nil
			}
		}
		for n := max(first, shown+1); n <= last; n++ {
			if !give(n, lineText(n), true) {
				return nil
			}
		}
		shown = max(shown, last)
		until := min(last+s.after, total)
		if i < len(matches) {
			until = min(until, lineOf(matches[i][0])-1)
		}
		for n := shown + 1; n <= until; n++ {
			if !give(n, lineText(n), false) {
				return nil
			}
			shown = n
		}
	}
	return nil
}

// show makes a line fit to print: bytes that are not text are replaced,
// and a line longer than maxColumns is cut. loc, when not nil, is where
// the line's first match is; a long line is cut around it so that the
// match is in what is shown.
func (s *scanSpec) show(line []byte, loc []int) string {
	if len(line) <= s.maxColumns {
		text, _ := showable(line)
		return text
	}
	from := 0
	if loc != nil && loc[0] > s.maxColumns*3/4 {
		from = loc[0] - s.maxColumns/4
		for from > 0 && !isRuneStart(line[from]) {
			from--
		}
	}
	to := min(from+s.maxColumns, len(line))
	for to > from && to < len(line) && !isRuneStart(line[to]) {
		to--
	}
	text, _ := showable(line[from:to])
	if from > 0 {
		text = fmt.Sprintf("[%d bytes ...] ", from) + text
	}
	if to < len(line) {
		text += fmt.Sprintf(" [... %d more bytes]", len(line)-to)
	}
	return text
}

// lineReader reads a file a line at a time without ever holding more than
// one line of at most max bytes, plus one read's worth.
type lineReader struct {
	r   io.Reader
	max int
	buf []byte
	// buf[pos:end] is what has been read and not yet handed out.
	pos, end int
	eof      bool
	// err is the read error that ended the file early, if one did.
	err error
}

// lineReadSize is how much is asked of the file at a time.
const lineReadSize = 64 << 10

// fill reads more of the file into the buffer, making room first. It
// reports whether it added anything.
func (lr *lineReader) fill() bool {
	if lr.eof {
		return false
	}
	if lr.pos > 0 {
		copy(lr.buf, lr.buf[lr.pos:lr.end])
		lr.end -= lr.pos
		lr.pos = 0
	}
	if lr.end == len(lr.buf) {
		grown := make([]byte, max(2*len(lr.buf), lineReadSize))
		copy(grown, lr.buf[:lr.end])
		lr.buf = grown
	}
	for {
		n, err := lr.r.Read(lr.buf[lr.end:])
		lr.end += n
		if err != nil {
			lr.eof = true
			if err != io.EOF {
				lr.err = err
			}
			return n > 0
		}
		if n > 0 {
			return true
		}
	}
}

// peek returns the start of what is waiting to be handed out, reading the
// first piece of the file if nothing has been read yet.
func (lr *lineReader) peek() []byte {
	if lr.end == lr.pos {
		lr.fill()
	}
	return lr.buf[lr.pos:lr.end]
}

// next hands out the next line, without its line break. terminated says
// whether a line break ended it: it is false for the last line of a file
// that does not end with one, and for each piece but the last of a line
// longer than max, which is handed out max bytes at a time. ok is false
// when the file is used up.
func (lr *lineReader) next() (line []byte, terminated bool, ok bool) {
	searched := 0
	for {
		waiting := lr.buf[lr.pos:lr.end]
		if i := bytes.IndexByte(waiting[searched:], '\n'); i >= 0 {
			i += searched
			if i > lr.max {
				// The line break is further off than one piece may be.
				lr.pos += lr.max
				return waiting[:lr.max], false, true
			}
			lr.pos += i + 1
			return waiting[:i], true, true
		}
		if len(waiting) >= lr.max {
			lr.pos += lr.max
			return waiting[:lr.max], false, true
		}
		searched = len(waiting)
		if !lr.fill() {
			// The file is used up. fill may have moved what was waiting,
			// so look at it afresh.
			waiting = lr.buf[lr.pos:lr.end]
			if len(waiting) == 0 {
				return nil, false, false
			}
			lr.pos = lr.end
			return waiting, false, true
		}
	}
}

// emptyAtEnd reports whether a match is the empty one after the line break
// that ends data. No line is there, as a search line by line finds none,
// so it is no match: (?m)^$ and ^\s*$ found it in nearly every file and
// pinned it on the last line (the search-tools review of 2026-11-20).
func emptyAtEnd(m []int, data []byte) bool {
	return m[0] == m[1] && m[0] == len(data) && len(data) > 0 && data[len(data)-1] == '\n'
}
