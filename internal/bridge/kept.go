package bridge

import (
	"regexp"
	"strings"
)

// This file is the kept facts of §10.6: what a pop must not lock away. A
// popped todo leaves its result and nothing else, so a wrong or unchecked
// conclusion in it cannot be revisited. For a host that asks (keep), the
// bridge keeps the task's own requirements, word for word, and what the
// model said it left unchecked, and shows them again once the model is
// back at the root after a pop.

const (
	// keptMaxLines and keptMaxChars bound the list, so that it stays short.
	keptMaxLines = 14
	keptMaxChars = 1600
	keptLineMax  = 300
)

var (
	bulletRe = regexp.MustCompile(`^\s*([*\-•]|\d+[.)])\s+`)
	// constraintRe is a word that makes a line a requirement or a limit.
	constraintRe = regexp.MustCompile(`(?i)\b(must|should|need to|at least|at most|exactly|only|never|do not|don't|without|between|minimum|maximum|required|requirements?|format|titled|named|ensure|make sure)\b`)
	// pathRe is a file path or a file name with an extension.
	pathRe = regexp.MustCompile("(?:^|[\\s`'\"(])(?:/|\\./|~/)?[\\w.\\-]+(?:/[\\w.\\-]+)+|\\b[\\w\\-]+\\.[a-zA-Z]{1,5}\\b")
	// limitRe is a word that sets a hard limit.
	limitRe = regexp.MustCompile(`(?i)\b(must|never|do not|at least|at most|exactly)\b`)
	// checkRe is a line that asks for something to be checked.
	checkRe = regexp.MustCompile(`(?i)\b(make sure|check|verify|ensure|confirm|validate|satisf(y|ies))\b`)
	// unsureRe marks what a model said it did not check or could not do.
	unsureRe = regexp.MustCompile(`(?i)\b(unchecked|not (yet )?(checked|verified|validated|confirmed|tested)|unverified|could not|couldn't|unable to|failed to|still need|assum(e|ed|ing)|todo)\b`)
)

// requirementLines reads a task's requirements from what the person wrote:
// every line that is a bullet or a number, holds a backtick, a digit, a
// path or a constraint word, word for word (a line longer than keptLineMax
// is cut). Lines that ask for a check come first, so the cap never drops
// them. It returns nothing for a task with no such line.
func requirementLines(task string) []string {
	var checks, strong, bare []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(task, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || seen[line] {
			continue
		}
		bullet := bulletRe.MatchString(line)
		hard := strings.Contains(line, "`") || strings.ContainsAny(line, "0123456789") || pathRe.MatchString(line) || constraintRe.MatchString(line)
		if !bullet && !hard {
			continue
		}
		// A long line that is not a bullet is the person's story, not a rule,
		// unless it asks for a check or sets a hard limit.
		if !bullet && len([]rune(line)) > 200 && !checkRe.MatchString(line) && !limitRe.MatchString(line) {
			continue
		}
		seen[line] = true
		if r := []rune(line); len(r) > keptLineMax {
			line = string(r[:keptLineMax]) + "…"
		}
		switch {
		case checkRe.MatchString(line):
			checks = append(checks, line)
		case hard:
			strong = append(strong, line)
		default:
			bare = append(bare, line)
		}
	}
	return append(append(checks, strong...), bare...)
}

// unsureOf is what a pop's result says it left unchecked: its sentences
// that name something not verified, not done or assumed.
func unsureOf(result string) []string {
	var out []string
	for _, s := range regexp.MustCompile(`[.\n;]\s*`).Split(result, -1) {
		s = strings.TrimSpace(s)
		if s != "" && unsureRe.MatchString(s) {
			if r := []rune(s); len(r) > 200 {
				s = string(r[:200]) + "…"
			}
			out = append(out, s)
		}
	}
	return out
}

// keptList is the list the model is shown: the task's requirement lines,
// the check lines marked as still to confirm, and what closed todos said
// they left unchecked. It is empty when there is nothing to keep.
func keptList(task string, unsure []string) string {
	reqs := requirementLines(task)
	if len(reqs) == 0 && len(unsure) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Kept from the task, word for word, since closed todos took their detail with them:\n")
	n, used := 0, b.Len()
	for _, l := range reqs {
		if n >= keptMaxLines || used+len(l) > keptMaxChars {
			break
		}
		mark := "- "
		if checkRe.MatchString(l) {
			mark = "- (still to confirm) "
		}
		b.WriteString(mark + bulletRe.ReplaceAllString(l, "") + "\n")
		n++
		used += len(l) + len(mark) + 1
	}
	for _, u := range unsure {
		if used+len(u) > keptMaxChars+400 {
			break
		}
		b.WriteString("- (left unchecked by a closed todo) " + u + "\n")
		used += len(u) + 40
	}
	return strings.TrimRight(b.String(), "\n")
}

// keepInstruction goes with the first push of a session: what a pop's
// result must carry, since only the result stays.
const keepInstruction = "When you pop it, put in its result, word for word: the task's exact requirements and constraints it touches, file paths, numbers and identifiers, and anything you have not checked or could not check, said plainly."
