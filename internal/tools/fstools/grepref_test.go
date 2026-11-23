package fstools

import (
	"fmt"
	"math/rand/v2"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file checks Grep against a search written the slow, obvious way:
// read every file whole, split it into lines, try the pattern on each
// line, and print. The two share nothing but the regexp package.

// refFile is one file of the tree the reference search knows about.
type refFile struct {
	path    string // full path, as Grep shows it
	name    string // base name
	content string
	seconds int // modification time, as setTime takes it
}

// refQuery is one Grep call, in the reference's terms.
type refQuery struct {
	pattern   string
	fold      bool
	mode      string
	before    int
	after     int
	only      bool
	numbers   bool
	multiline bool
	limit     int // -1 means the call gives none
	offset    int
	glob      string // "", "*.txt" or "*.md"
}

// input is the query as arguments to the Grep tool.
func (q refQuery) input() in {
	input := in{"pattern": q.pattern, "output_mode": q.mode}
	if q.fold {
		input["-i"] = true
	}
	if q.before > 0 {
		input["-B"] = q.before
	}
	if q.after > 0 {
		input["-A"] = q.after
	}
	if q.only {
		input["-o"] = true
	}
	if !q.numbers {
		input["-n"] = false
	}
	if q.multiline {
		input["multiline"] = true
	}
	if q.limit >= 0 {
		input["head_limit"] = q.limit
	}
	if q.offset > 0 {
		input["offset"] = q.offset
	}
	if q.glob != "" {
		input["glob"] = q.glob
	}
	return input
}

// refEntry is one line of a content result.
type refEntry struct {
	file  int
	num   int
	text  string
	match bool
}

// refLines splits a file into its lines as a reader sees them: no byte
// order mark, no line breaks, no carriage return before a line break.
func refLines(content string) []string {
	content = strings.TrimPrefix(content, utf8BOM)
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	ended := lines[len(lines)-1] == ""
	if ended {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		if i < len(lines)-1 || ended {
			lines[i] = strings.TrimSuffix(lines[i], "\r")
		}
	}
	return lines
}

// refSearch searches one file the slow way. It returns the count the
// file would have in count mode and the lines it would have in content
// mode.
func refSearch(file int, content string, re *regexp.Regexp, q refQuery) (count int, entries []refEntry) {
	lines := refLines(content)
	matched := make([]bool, len(lines))
	if q.multiline {
		// Line breaks only, and a line break after every line but perhaps
		// the last.
		text := strings.ReplaceAll(strings.TrimPrefix(content, utf8BOM), "\r\n", "\n")
		if text == "" {
			return 0, nil
		}
		lineOf := func(off int) int {
			return min(strings.Count(text[:off], "\n")+1, len(lines))
		}
		for _, m := range re.FindAllStringIndex(text, -1) {
			count++
			for l := lineOf(m[0]); l <= lineOf(max(m[0], m[1]-1)); l++ {
				matched[l-1] = true
			}
		}
	} else {
		for i, line := range lines {
			if re.MatchString(line) {
				matched[i] = true
				count++
			}
		}
	}
	if q.only {
		for i, line := range lines {
			for _, m := range re.FindAllString(line, -1) {
				if m != "" {
					entries = append(entries, refEntry{file: file, num: i + 1, text: m, match: true})
				}
			}
		}
		return count, entries
	}
	show := make([]bool, len(lines))
	for i := range lines {
		if !matched[i] {
			continue
		}
		for j := max(i-q.before, 0); j <= min(i+q.after, len(lines)-1); j++ {
			show[j] = true
		}
	}
	for i, line := range lines {
		if show[i] {
			entries = append(entries, refEntry{file: file, num: i + 1, text: line, match: matched[i]})
		}
	}
	return count, entries
}

// refNote is the note that says what part of a result was shown.
func refNote(what string, offset, shown, total int) string {
	first := min(offset, total)
	switch {
	case shown == 0:
		return fmt.Sprintf("[Nothing to show at offset %d: there are only %d %s. Use a smaller offset.]\n", offset, total, what)
	case first+shown < total:
		return fmt.Sprintf("[Showing %s %d-%d of %d. To see more, use offset=%d.]\n", what, first+1, first+shown, total, first+shown)
	case first > 0:
		return fmt.Sprintf("[Showing %s %d-%d of %d: these are the last.]\n", what, first+1, first+shown, total)
	}
	return ""
}

// refGrep is the whole reference search: what Grep should print for the
// query over the files under root.
func refGrep(t *testing.T, root string, files []refFile, q refQuery) string {
	t.Helper()
	expr := q.pattern
	switch {
	case q.fold && q.multiline:
		expr = "(?im)" + expr
	case q.fold:
		expr = "(?i)" + expr
	case q.multiline:
		expr = "(?m)" + expr
	}
	re := regexp.MustCompile(expr)

	// The files in the order Grep lists them: files with a match newest
	// first, then by path; lines and counts by path alone.
	var searched []refFile
	for _, f := range files {
		if q.glob == "" || strings.HasSuffix(f.name, strings.TrimPrefix(q.glob, "*")) {
			searched = append(searched, f)
		}
	}
	sort.SliceStable(searched, func(i, j int) bool {
		if q.mode == "files_with_matches" && searched[i].seconds != searched[j].seconds {
			return searched[i].seconds > searched[j].seconds
		}
		return searched[i].path < searched[j].path
	})

	type hit struct {
		path    string
		count   int
		entries []refEntry
	}
	var hits []hit
	for i, f := range searched {
		q := q
		if q.mode != "content" {
			q.only = false
		}
		count, entries := refSearch(i, f.content, re, q)
		if count > 0 {
			hits = append(hits, hit{f.path, count, entries})
		}
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No matches found. %s searched under %s.\n", countOf(len(searched), "file was", "files were"), root)
	}
	limit := q.limit
	if limit < 0 {
		limit = 250
	}
	page := func(total int) (first, count int) {
		first = min(q.offset, total)
		count = total - first
		if limit > 0 {
			count = min(count, limit)
		}
		return first, count
	}
	withNote := func(body, note string) string {
		if note == "" {
			return body
		}
		if body == "" {
			return note
		}
		return body + "\n" + note
	}

	var out strings.Builder
	switch q.mode {
	case "files_with_matches":
		first, count := page(len(hits))
		for _, h := range hits[first : first+count] {
			out.WriteString(h.path + "\n")
		}
		return withNote(out.String(), refNote("files", q.offset, count, len(hits)))
	case "count":
		first, count := page(len(hits))
		total := 0
		for _, h := range hits {
			total += h.count
		}
		for _, h := range hits[first : first+count] {
			fmt.Fprintf(&out, "%s:%d\n", h.path, h.count)
		}
		if count > 0 {
			out.WriteString("\n")
		}
		what := countOf(total, "matching line", "matching lines")
		if q.multiline {
			what = countOf(total, "match", "matches")
		}
		fmt.Fprintf(&out, "Total: %s in %s.\n", what, countOf(len(hits), "file", "files"))
		return withNote(out.String(), refNote("files", q.offset, count, len(hits)))
	}

	// Content: every line of every file in one list, then the page of it.
	var all []refEntry
	paths := make(map[int]string)
	for _, h := range hits {
		if len(h.entries) > 0 {
			paths[h.entries[0].file] = h.path
		}
		all = append(all, h.entries...)
	}
	if len(all) == 0 {
		return fmt.Sprintf("The pattern matched in %s, but only empty text, so there is nothing to show.\n", countOf(len(hits), "file", "files"))
	}
	first, count := page(len(all))
	breaks := !q.only && (q.before > 0 || q.after > 0)
	for i, e := range all[first : first+count] {
		prev := refEntry{file: -1}
		if i > 0 {
			prev = all[first+i-1]
		}
		switch {
		case e.file != prev.file:
			if i > 0 {
				out.WriteString("\n")
			}
			out.WriteString(paths[e.file] + "\n")
		case breaks && e.num > prev.num+1:
			out.WriteString("--\n")
		}
		if q.numbers {
			if e.match {
				fmt.Fprintf(&out, "%d:", e.num)
			} else {
				fmt.Fprintf(&out, "%d-", e.num)
			}
		}
		out.WriteString(e.text + "\n")
	}
	return withNote(out.String(), refNote("result lines", q.offset, count, len(all)))
}

// refTree makes a tree of small text files with words that repeat, and
// returns what it made.
func refTree(f *fixture, r *rand.Rand) []refFile {
	f.t.Helper()
	words := []string{"alpha", "beta", "gamma", "delta", "one", "two", "three", "foo", "bar", "baz",
		"Alpha", "BETA", "x", "y", "xx", "(paren)", "a.c", "abc", "foo bar", "end"}
	dirs := []string{"", "src/", "src/pkg/", "src/pkg/deep/", "docs/", "a/b/c/d/"}
	var files []refFile
	for i := 0; i < 48; i++ {
		ext := ".txt"
		if i%3 == 0 {
			ext = ".md"
		}
		name := fmt.Sprintf("f%02d%s", i, ext)
		rel := dirs[r.IntN(len(dirs))] + name
		var b strings.Builder
		for l, n := 0, r.IntN(40); l < n; l++ {
			for w, k := 0, r.IntN(5); w < k; w++ {
				if w > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(words[r.IntN(len(words))])
			}
			b.WriteByte('\n')
		}
		content := b.String()
		switch {
		case i == 0:
			content = ""
		case i%7 == 1:
			content = strings.TrimSuffix(content, "\n")
		case i%7 == 2:
			content = strings.ReplaceAll(content, "\n", "\r\n")
		case i%7 == 3:
			content = utf8BOM + content
		}
		seconds := r.IntN(12)
		f.setTime(f.file(rel, content), seconds)
		files = append(files, refFile{path: f.path(rel), name: name, content: content, seconds: seconds})
	}
	return files
}

func TestGrepAgainstReference(t *testing.T) {
	f := newFixture(t, Options{GrepMaxOutputBytes: 1 << 30})
	seed := uint64(1113)
	// GNOSH_GREP_SEED and GNOSH_GREP_ROUNDS let a person run another or a
	// longer comparison by hand; the suite itself never sets them.
	if n, err := strconv.ParseUint(os.Getenv("GNOSH_GREP_SEED"), 10, 64); err == nil {
		seed = n
	}
	r := rand.New(rand.NewPCG(2026, seed))
	files := refTree(f, r)

	patterns := []string{
		"alpha", "be+ta", "^gamma", "delta$", "a.c", "ALPHA", `\bone\b`, "x|y", "x*", "^$", "[A-Z]",
		`\(paren\)`, "foo bar", "zzzz", `\w+a\b`, "^one two", "end$", `^\w+$`, "o", `(alpha|beta) (gamma|delta)`,
	}
	across := []string{
		`alpha\n`, `one\ntwo`, `(?s)foo.*?bar`, "^beta$", `\n\n`, "gamma", `a\nb`, `end\n\w+`, `\n$`, `^\n`, `(?s)\A.`, `(?s).\z`,
	}
	modes := []string{"files_with_matches", "content", "count"}
	globs := []string{"", "", "*.txt", "*.md"}

	rounds := 800
	if testing.Short() {
		rounds = 150
	}
	if n, err := strconv.Atoi(os.Getenv("GNOSH_GREP_ROUNDS")); err == nil && n > 0 {
		rounds = n
	}
	for round := 0; round < rounds; round++ {
		q := refQuery{
			mode:    modes[r.IntN(len(modes))],
			fold:    r.IntN(4) == 0,
			numbers: r.IntN(5) != 0,
			limit:   -1,
			glob:    globs[r.IntN(len(globs))],
		}
		if r.IntN(4) == 0 {
			q.multiline = true
			q.pattern = across[r.IntN(len(across))]
		} else {
			q.pattern = patterns[r.IntN(len(patterns))]
		}
		if r.IntN(2) == 0 {
			q.before = r.IntN(4)
		}
		if r.IntN(2) == 0 {
			q.after = r.IntN(4)
		}
		if q.mode == "content" && !q.multiline && r.IntN(5) == 0 {
			q.only = true
		}
		if r.IntN(2) == 0 {
			q.limit = r.IntN(30)
		}
		if r.IntN(3) == 0 {
			q.offset = r.IntN(60)
		}
		want := refGrep(t, f.dir, files, q)
		got := f.ok(f.grep, q.input())
		if got != want {
			t.Fatalf("round %d: Grep(%v)\n--- got ---\n%s\n--- want ---\n%s", round, q.input(), got, want)
		}
	}
}
