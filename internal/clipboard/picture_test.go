package clipboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPictureTool(t *testing.T) {
	look := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", nil, "/usr/bin/osascript"},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "/usr/bin/wl-paste"},
		{"linux", map[string]string{"DISPLAY": ":0"}, "/usr/bin/xclip"},
		{"linux", nil, ""},
		{"darwin", map[string]string{"SSH_CONNECTION": "203.0.113.5 50000 203.0.113.6 22"}, ""},
		{"windows", nil, ""},
	} {
		tool := PictureTool(c.goos, env(c.env), look)
		got := ""
		if len(tool) > 0 {
			got = tool[0]
		}
		if got != c.want {
			t.Errorf("%s %v: %v, want %s", c.goos, c.env, tool, c.want)
		}
	}
}

// A tool that prints a PNG gives the picture; one that prints something
// else, or fails, gives ErrNoPicture.
func TestReadPicture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no shell scripts here")
	}
	dir := t.TempDir()
	png := append([]byte("\x89PNG\r\n\x1a\n"), "the rest"...)
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	script := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	got, err := ReadPicture(context.Background(), []string{script("xclip", "cat "+filepath.Join(dir, "pic.png"))}, nil)
	if err != nil || !bytes.Equal(got, png) {
		t.Errorf("a picture: %q, %v", got, err)
	}
	if _, err := ReadPicture(context.Background(), []string{script("wl-paste", "echo just words")}, nil); !errors.Is(err, ErrNoPicture) {
		t.Errorf("words: %v, want ErrNoPicture", err)
	}
	if _, err := ReadPicture(context.Background(), []string{script("xclip2", "echo 'target image/png not available' >&2; exit 1")}, nil); !errors.Is(err, ErrNoPicture) {
		t.Errorf("a failing tool: %v, want ErrNoPicture", err)
	}
}

func TestAppleString(t *testing.T) {
	if got := appleString(`/tmp/a "b"\c`); got != `"/tmp/a \"b\"\\c"` {
		t.Errorf("appleString = %s", got)
	}
}
