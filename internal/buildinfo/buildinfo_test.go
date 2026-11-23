package buildinfo

import (
	"runtime/debug"
	"testing"
)

func settings(kv ...string) []debug.BuildSetting {
	var out []debug.BuildSetting
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func TestFromBuildInfo(t *testing.T) {
	t.Parallel()
	const commit = "04c613f0a1b2c3d4e5f60718293a4b5c6d7e8f90"
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"no build info", nil, "devel"},
		{"nothing recorded", &debug.BuildInfo{}, "devel"},
		{"a version the go command does not know", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "devel"},
		{
			"a clean commit",
			&debug.BuildInfo{Settings: settings("vcs", "git", "vcs.revision", commit, "vcs.modified", "false")},
			"04c613f0a1b2",
		},
		{
			"a commit with uncommitted changes",
			&debug.BuildInfo{Settings: settings("vcs.revision", commit, "vcs.modified", "true")},
			"04c613f0a1b2+dirty",
		},
		{
			"a revision shorter than the cut",
			&debug.BuildInfo{Settings: settings("vcs.revision", "1234abc")},
			"1234abc",
		},
		{
			"the commit names the build even when the module has a version",
			&debug.BuildInfo{
				Main:     debug.Module{Version: "v0.0.0-20261113091500-04c613f0a1b2+dirty"},
				Settings: settings("vcs.revision", commit, "vcs.modified", "true"),
			},
			"04c613f0a1b2+dirty",
		},
		{
			"installed from a module proxy",
			&debug.BuildInfo{Main: debug.Module{Path: "github.com/gnosh-ai/gnosh", Version: "v1.2.3"}},
			"v1.2.3",
		},
		{
			"changes without a commit say nothing",
			&debug.BuildInfo{Settings: settings("vcs.modified", "true")},
			"devel",
		},
	} {
		if got := fromBuildInfo(tc.info); got != tc.want {
			t.Errorf("%s: fromBuildInfo = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	recorded := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Settings: settings("vcs.revision", "abcdef0123456789")}, true
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	unread := func() (*debug.BuildInfo, bool) {
		t.Error("the build info was read though the linker named the build")
		return nil, false
	}
	for _, tc := range []struct {
		name   string
		linked string
		read   func() (*debug.BuildInfo, bool)
		want   string
	}{
		{"the linker's name wins", "gnovm-it", unread, "gnovm-it"},
		{"the linker's name is trimmed", " nightly-7\n", unread, "nightly-7"},
		{"an empty name falls back to the build info", "", recorded, "abcdef012345"},
		{"a blank name falls back to the build info", "  ", recorded, "abcdef012345"},
		{"no name and no build info", "", none, "devel"},
		{
			"build info that says it is not there is not used",
			"",
			func() (*debug.BuildInfo, bool) {
				return &debug.BuildInfo{Settings: settings("vcs.revision", "abcdef0123456789")}, false
			},
			"devel",
		},
	} {
		if got := resolve(tc.linked, tc.read); got != tc.want {
			t.Errorf("%s: resolve = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Whatever the build, BuildID names it by the time anything runs: here, a
// test binary, whether or not the linker was told a name for it.
func TestBuildIDIsSetAtStart(t *testing.T) {
	t.Parallel()
	if BuildID == "" {
		t.Fatal("BuildID is empty")
	}
}

// A build's list of changes, and what of it is newer than another build.
func TestChanges(t *testing.T) {
	t.Parallel()
	all := parseChanges("1be72ee /status says the thinking budget\nfd3e5e3 DECISIONS 74 and 75\n\n93da8ee Print mode to a terminal\n")
	if len(all) != 3 || all[1] != (Change{ID: "fd3e5e3", Subject: "DECISIONS 74 and 75"}) {
		t.Fatalf("parseChanges = %+v", all)
	}
	for _, tc := range []struct {
		old   string
		n     int
		found bool
	}{
		{"93da8ee", 2, true},
		{"93da8ee4c1d2", 2, true},
		{"1be72ee", 0, true},
		{"fd3e5e3+dirty", 1, true},
		{"0000000", 3, false},
		{"", 3, false},
	} {
		got, found := since(all, tc.old)
		if len(got) != tc.n || found != tc.found {
			t.Errorf("since(%q) = %d changes, found %v; want %d, %v", tc.old, len(got), found, tc.n, tc.found)
		}
	}
	if len(parseChanges("")) != 0 {
		t.Errorf("an empty list holds changes")
	}
}
