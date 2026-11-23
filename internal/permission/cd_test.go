package permission

import (
	"strings"
	"testing"
)

// Cd rules say where the person's /cd may move the session, as Claude
// Code's do: a bare deny switches /cd off, a deny with a pattern keeps
// it out of the folders covered (as typed, by way of a link, or as
// resolved), and any allow rule makes the allow rules a list of the only
// folders it may go to. Patterns are matched against a folder's whole
// path: * is one segment, ** any number, a trailing /** takes in the
// folder named, and a pattern with no anchor starts at the working
// directory (/proj in these tests).
func TestCdRules(t *testing.T) {
	tests := []struct {
		name  string
		r     rules
		names []string
		want  string // a piece of the refusal, or "" for none
	}{
		{"no Cd rules", rules{}, []string{"/anywhere"}, ""},
		{"rules for tools only", rules{deny: []string{"Read(/x/**)"}, allow: []string{"Bash(ls)"}}, []string{"/x"}, ""},
		{"a bare deny", rules{deny: []string{"Cd"}}, []string{"/proj/sub"}, "The deny rule Cd from user settings switches /cd off."},
		{"a deny that covers it", rules{deny: []string{"Cd(~/secret)"}}, []string{"/home/u/secret"}, "The deny rule Cd(~/secret) from user settings covers /home/u/secret."},
		{"a deny of one folder leaves the ones in it", rules{deny: []string{"Cd(~/secret)"}}, []string{"/home/u/secret/inner"}, ""},
		{"a deny with ** takes in the folder and those in it", rules{deny: []string{"Cd(~/secret/**)"}}, []string{"/home/u/secret/inner"}, "covers /home/u/secret/inner"},
		{"a trailing /** takes in its root", rules{deny: []string{"Cd(~/secret/**)"}}, []string{"/home/u/secret"}, "covers /home/u/secret"},
		{"a deny by way of a link", rules{deny: []string{"Cd(//etc)"}}, []string{"/home/u/link", "/etc"}, "covers /etc"},
		{"a deny of the link's own name", rules{deny: []string{"Cd(~/link)"}}, []string{"/home/u/link", "/etc"}, "covers /home/u/link"},
		{"a deny goes by names as the disk may fold them", rules{deny: []string{"Cd(~/Secret)"}}, []string{"/home/u/SECRET"}, "covers /home/u/SECRET"},
		{"a relative deny at any depth", rules{deny: []string{"Cd(**/node_modules)"}}, []string{"/proj/a/b/node_modules"}, "covers"},
		{"a relative deny is in the working directory only", rules{deny: []string{"Cd(**/node_modules)"}}, []string{"/other/node_modules"}, ""},
		{"a relative deny names no folder below it", rules{deny: []string{"Cd(**/node_modules)"}}, []string{"/proj/node_modules/pkg"}, ""},
		{"an allow list that covers it", rules{allow: []string{"Cd(~/code/*)"}}, []string{"/home/u/code/app"}, ""},
		{"* is one segment", rules{allow: []string{"Cd(~/code/*)"}}, []string{"/home/u/code/app/src"}, "The Cd allow rules name the folders /cd may go to (Cd(~/code/*)), and /home/u/code/app/src is none of them."},
		{"* is not the folder itself", rules{allow: []string{"Cd(~/code/*)"}}, []string{"/home/u/code"}, "is none of them"},
		{"** is any depth", rules{allow: []string{"Cd(~/code/**)"}}, []string{"/home/u/code/app/src"}, ""},
		{"an allow goes by the folder as it resolves", rules{allow: []string{"Cd(~/code/**)"}}, []string{"/home/u/code/link", "/etc"}, "/etc is none of them"},
		{"a bare allow lets /cd go anywhere", rules{allow: []string{"Cd", "Cd(~/code/*)"}}, []string{"/var/tmp"}, ""},
		{"a deny beats an allow", rules{allow: []string{"Cd(~/code/**)"}, deny: []string{"Cd(~/code/secret)"}}, []string{"/home/u/code/secret"}, "covers /home/u/code/secret"},
		{"// is the root", rules{allow: []string{"Cd(//srv/*)"}}, []string{"/srv/www"}, ""},
		{"a slash at the end changes nothing", rules{allow: []string{"Cd(~/code/)"}}, []string{"/home/u/code"}, ""},
		{"a slash at the end does not take in the folders below", rules{allow: []string{"Cd(~/code/)"}}, []string{"/home/u/code/app"}, "is none of them"},
		{"one slash is the settings file's folder", rules{allow: []string{"Cd(/work)"}}, []string{"/home/u/.config/gnosh/work"}, ""},
	}
	for _, tt := range tests {
		e := engine(t, ModeDefault, tt.r)
		got := e.CdRefusal(tt.names)
		switch {
		case tt.want == "" && got != "":
			t.Errorf("%s: refused: %s", tt.name, got)
		case tt.want != "" && !strings.Contains(got, tt.want):
			t.Errorf("%s: got %q, want it to hold %q", tt.name, got, tt.want)
		}
	}

	// A held allow rule (a folder not trusted) makes no list; a held deny
	// rule still keeps /cd out.
	o := testOptions(ModeDefault, rules{})
	o.Trusted = false
	o.Layers = append(o.Layers, Layer{Scope: ScopeProject, Name: "project settings", Base: "/proj/.gnosh", Allow: []string{"Cd(//nowhere)"}, Deny: []string{"Cd(//etc)"}})
	e := mustEngine(t, o)
	if got := e.CdRefusal([]string{"/var/tmp"}); got != "" {
		t.Errorf("a held allow rule made a list: %s", got)
	}
	if got := e.CdRefusal([]string{"/etc"}); !strings.Contains(got, "covers /etc") {
		t.Errorf("a held deny rule was passed over: %q", got)
	}

	// There are no Cd ask rules, and a Cd pattern is read as a path one.
	for _, tt := range []struct{ list, rule, want string }{
		{"ask", "Cd", "nobody else to ask"},
		{"ask", "Cd(~/x)", "nobody else to ask"},
		{"allow", "Cd(../x)", "\"..\""},
		{"deny", "Cd(~)", ""},
	} {
		o := testOptions(ModeDefault, rules{})
		switch tt.list {
		case "ask":
			o.Layers[0].Ask = []string{tt.rule}
		case "allow":
			o.Layers[0].Allow = []string{tt.rule}
		case "deny":
			o.Layers[0].Deny = []string{tt.rule}
		}
		_, err := New(o)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s %s: %v", tt.list, tt.rule, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s %s: got %v, want an error holding %q", tt.list, tt.rule, err, tt.want)
		}
	}

	// Cd rules decide nothing about tools: a Cd allow rule is no Read rule.
	e = engine(t, ModeDefault, rules{allow: []string{"Cd(//etc/**)"}})
	if d := file(e, "Read", "/etc/passwd"); d.Behavior == Allow {
		t.Errorf("a Cd allow rule let Read run: %+v", d)
	}
}
