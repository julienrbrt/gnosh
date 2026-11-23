package app

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gnosh-ai/gnosh/internal/agent"
	"github.com/gnosh-ai/gnosh/internal/shellquote"
	"github.com/gnosh-ai/gnosh/internal/tools/shelltool"
)

// This file is Claude Code's rule that a file viewed with a shell command
// counts as read (its tools reference, "Edit tool behavior"): cat, nl,
// bat, batcat, head, tail, sed -n 'X,Yp', grep, egrep, fgrep or rg, run
// on one file with no pipe or redirection, that printed some of it. The
// file may then be edited or written over without a separate Read, and
// the instruction files of its folder and the rules that name it arrive
// as after a Read. A small model reads with cat as often as with Read,
// and its Edit was refused until it read the file again.

// sedPrint is the script of sed -n that prints lines X to Y: 10,20p, 5p,
// 10,$p.
var sedPrint = regexp.MustCompile(`^[0-9]+(,([0-9]+|\$))?p$`)

// grepValueFlags are the options of grep and rg that take the word after
// them as their value. A command with one of them is not counted: what
// is the pattern and what is the file is then not plain to read.
var grepValueFlags = map[string]bool{
	"-e": true, "-f": true, "-m": true, "-A": true, "-B": true, "-C": true, "-d": true, "-D": true,
	"-g": true, "-t": true, "-T": true, "-M": true, "-j": true,
	"--regexp": true, "--file": true, "--max-count": true, "--glob": true, "--type": true,
	"--after-context": true, "--before-context": true, "--context": true,
}

// shellRead is the file a Bash call viewed, as Claude Code counts it,
// absolute, or "": a command of the readers above on one file of this
// machine, run in the foreground, that ended well and printed something.
func shellRead(done agent.ToolDone) string {
	if done.Call.Name != shelltool.BashName || done.Result.IsError {
		return ""
	}
	var in struct {
		Command    string `json:"command"`
		Background bool   `json:"run_in_background"`
	}
	if json.Unmarshal(done.Call.Input, &in) != nil || in.Background {
		return ""
	}
	meta := done.Result.Meta
	if host, _ := meta["host"].(string); host != "" && host != shelltool.LocalName {
		return ""
	}
	if code, ok := wholeNumber(meta["exitCode"]); !ok || code != 0 {
		return ""
	}
	if n, ok := wholeNumber(meta["outputBytes"]); !ok || n <= 0 {
		return ""
	}
	words, err := shellquote.Split(strings.TrimSpace(in.Command))
	if err != nil || len(words) < 2 {
		return ""
	}
	file := viewedFile(words)
	if file == "" {
		return ""
	}
	if !filepath.IsAbs(file) {
		dir, _ := meta["cwd"].(string)
		if dir == "" {
			return ""
		}
		file = filepath.Join(dir, file)
	}
	return filepath.Clean(file)
}

// viewedFile is the one file a reader's words name, or "".
func viewedFile(words []string) string {
	args := words[1:]
	var files []string
	switch filepath.Base(words[0]) {
	case "cat", "nl", "bat", "batcat":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") || a == "-" {
				files = append(files, a)
			}
		}
	case "head", "tail":
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "-n" || a == "-c" || a == "--lines" || a == "--bytes":
				i++ // (its value)
			case a == "-f" || a == "-F" || strings.HasPrefix(a, "--follow"):
				return "" // (what follows a file is no view of it)
			case strings.HasPrefix(a, "-") && a != "-":
			default:
				files = append(files, a)
			}
		}
	case "sed":
		if len(args) != 3 || args[0] != "-n" || !sedPrint.MatchString(args[1]) {
			return ""
		}
		files = args[2:]
	case "grep", "egrep", "fgrep", "rg":
		pattern := false
		for _, a := range args {
			switch {
			case grepValueFlags[a]:
				return ""
			case strings.HasPrefix(a, "-") && a != "-":
			case !pattern:
				pattern = true
			default:
				files = append(files, a)
			}
		}
	default:
		return ""
	}
	if len(files) != 1 || files[0] == "-" {
		return ""
	}
	return files[0]
}

// wholeNumber reads a whole number a tool result's Meta holds: an int, as
// the tool put it, or a number as JSON gives one back.
func wholeNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), n == float64(int64(n))
	}
	return 0, false
}
