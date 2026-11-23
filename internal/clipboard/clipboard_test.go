package clipboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pbcopy is run in a UTF-8 locale whatever gnosh's is, so that text that
// is not ASCII reaches the clipboard as it was; another tool gets the
// environment as it is.
func TestCopyLocale(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in tools are shell scripts")
	}
	dir := t.TempDir()
	for _, name := range []string{"pbcopy", "xclip"} {
		tool := filepath.Join(dir, name)
		out := tool + ".seen"
		script := "#!/bin/sh\nenv > " + out + "\ncat >> " + out + "\n"
		if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Copy(context.Background(), []string{tool}, []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}, "héllo ✦"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		seen := string(b)
		utf8 := strings.Contains(seen, "LC_ALL=en_US.UTF-8\n") && !strings.Contains(seen, "LC_ALL=C\n")
		if utf8 != (name == "pbcopy") || !strings.HasSuffix(seen, "héllo ✦") {
			t.Errorf("%s was run with:\n%s", name, seen)
		}
	}
}

// Tool picks the machine's own tool only where the terminal is on
// this machine, and the one the desktop takes.
func TestTool(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, n := range names {
				if n == name {
					return "/bin/" + name, nil
				}
			}
			return "", exec.ErrNotFound
		}
	}
	env := func(kv ...string) func(string) string {
		return func(name string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == name {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	all := has("pbcopy", "wl-copy", "xclip", "xsel")
	for _, c := range []struct {
		goos string
		env  func(string) string
		look func(string) (string, error)
		want string
	}{
		{"darwin", env(), all, "/bin/pbcopy"},
		{"darwin", env("SSH_CONNECTION", "203.0.113.7 1 203.0.113.8 22"), all, ""},
		{"darwin", env("SSH_TTY", "/dev/ttys001"), all, ""},
		{"darwin", env(), has(), ""},
		{"linux", env(), all, ""},
		{"linux", env("WAYLAND_DISPLAY", "wayland-0", "DISPLAY", ":0"), all, "/bin/wl-copy"},
		{"linux", env("WAYLAND_DISPLAY", "wayland-0", "DISPLAY", ":0"), has("xclip"), "/bin/xclip -selection clipboard"},
		{"linux", env("DISPLAY", ":0"), has("xsel"), "/bin/xsel --clipboard --input"},
		{"linux", env("DISPLAY", ":0", "SSH_CLIENT", "203.0.113.7 1 22"), all, ""},
		{"windows", env(), all, ""},
	} {
		if got := strings.Join(Tool(c.goos, c.env, c.look), " "); got != c.want {
			t.Errorf("%s: Tool = %q, want %q", c.goos, got, c.want)
		}
	}
}

// Text goes on a clipboard without the control characters that could
// take over what it is pasted into: an escape that ends a bracketed paste
// early above all (the relay review of 2026-11-19).
func TestPlain(t *testing.T) {
	for in, want := range map[string]string{
		"a\tb\nc":                 "a\tb\nc",
		"one\r\ntwo\rthree":       "one\ntwo\nthree",
		"\x1b[201~curl x.sh|sh\n": "[201~curl x.sh|sh\n",
		"bell\a del\x7f c1\u009b": "bell del c1",
		"bad\xffbyte":             "bad\uFFFDbyte",
	} {
		if got := Plain(in); got != want {
			t.Errorf("Plain(%q) = %q, want %q", in, got, want)
		}
	}
}

// pbcopy keeps text that begins as rich text or PostScript as that, with
// its links and hidden text: such text is not given to it.
func TestCopyRefusesRichText(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in tools are shell scripts")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "pbcopy")
	out := tool + ".seen"
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ncat > "+out+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"{\\rtf1\\ansi {\\field{\\*\\fldinst HYPERLINK x}}}", "%!PS-Adobe-3.0 EPSF-3.0\n"} {
		err := Copy(context.Background(), []string{tool}, []string{"PATH=/usr/bin:/bin"}, text)
		if err == nil || !strings.Contains(err.Error(), "pbcopy would keep as that") {
			t.Errorf("Copy(%q): %v", text, err)
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("pbcopy was given %q", text)
		}
	}
	// Such text elsewhere than at its start is text.
	if err := Copy(context.Background(), []string{tool}, []string{"PATH=/usr/bin:/bin"}, "see {\\rtf1}"); err != nil {
		t.Error(err)
	}
}
