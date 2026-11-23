package envscrub

import (
	"slices"
	"strings"
	"testing"
)

// The scrub as Claude Code's documents it: credentials go by their names
// or their values; the GitHub tokens, the proxies and git's GIT_CONFIG_*
// stay; a package index keeps its URL without the name and password; a
// file's path, and what is no credential, stays.
func TestEnv(t *testing.T) {
	in := []string{
		"ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnop1234", "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE", "NPM_TOKEN=abc", "DB_PASSWORD=hunter2", "OPENAI_KEY=0123456789abcdef0123",
		"DATABASE_URL=postgres://app:hunter2@db.example/app", "REDIS_URL=redis://:pw@cache.example:6379",
		"PIP_INDEX_URL=https://user:pw@pypi.example/simple", "NPM_CONFIG_REGISTRY=https://tok@npm.example/",
		"CLAUDE_CONFIG_DIR=/home/u/.claude", "GNOSH_CONFIG_DIR=/home/u/.config/gnosh",
		"GITHUB_TOKEN=ghp_abcdefghijklmnop123456", "GH_TOKEN=gho_abcdefghijklmnop123456",
		"HTTPS_PROXY=http://u:p@proxy.example:3128", "http_proxy=http://u:p@proxy.example:3128",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Bearer abc",
		"MY_THING=ghp_abcdefghijklmnop123456", "SESSION=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"DEPLOY=-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
		"PATH=/usr/bin:/bin", "HOME=/home/u", "PWD=/home/u/p", "MAX_TOKENS=4096", "TOKENIZERS_PARALLELISM=false",
		"SSH_AUTH_SOCK=/tmp/ssh-x/agent.1", "GOOGLE_APPLICATION_CREDENTIALS=/home/u/key.json", "PASSWORD_STORE_DIR=/home/u/.password-store",
		"SORT_KEY=name", "TASK=sk-learn", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1", "EMPTY_TOKEN=",
	}
	got := Env(in)
	gone := []string{"ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "NPM_TOKEN", "DB_PASSWORD", "OPENAI_KEY",
		"DATABASE_URL", "REDIS_URL", "CLAUDE_CONFIG_DIR", "GNOSH_CONFIG_DIR", "MY_THING", "SESSION", "DEPLOY"}
	for _, name := range gone {
		if slices.ContainsFunc(got, func(kv string) bool { return strings.HasPrefix(kv, name+"=") }) {
			t.Errorf("%s was left in", name)
		}
	}
	for _, kv := range []string{
		"PIP_INDEX_URL=https://pypi.example/simple", "NPM_CONFIG_REGISTRY=https://npm.example/",
		"GITHUB_TOKEN=ghp_abcdefghijklmnop123456", "GH_TOKEN=gho_abcdefghijklmnop123456",
		"HTTPS_PROXY=http://u:p@proxy.example:3128", "http_proxy=http://u:p@proxy.example:3128",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Bearer abc",
		"PATH=/usr/bin:/bin", "HOME=/home/u", "PWD=/home/u/p", "MAX_TOKENS=4096", "TOKENIZERS_PARALLELISM=false",
		"SSH_AUTH_SOCK=/tmp/ssh-x/agent.1", "GOOGLE_APPLICATION_CREDENTIALS=/home/u/key.json", "PASSWORD_STORE_DIR=/home/u/.password-store",
		"SORT_KEY=name", "TASK=sk-learn", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1", "EMPTY_TOKEN=",
	} {
		if !slices.Contains(got, kv) {
			t.Errorf("%q is not in what is left:\n%s", kv, strings.Join(got, "\n"))
		}
	}
}

// The switch: 1, true, yes or on, the last setting winning.
func TestOn(t *testing.T) {
	for env, want := range map[string]bool{"": false, Var + "=1": true, Var + "=true": true, Var + "=0": false, Var + "=1\n" + Var + "=0": false, "X=1": false} {
		if got := On(strings.Split(env, "\n")); got != want {
			t.Errorf("On(%q) = %v, want %v", env, got, want)
		}
	}
}

// The baseline an MCP server starts from with CLAUDE_CODE_MCP_ALLOWLIST_ENV
// on: what a program needs to run as the person, and nothing else.
func TestBaseline(t *testing.T) {
	got := Baseline([]string{"PATH=/usr/bin", "HOME=/home/u", "LANG=en_US.UTF-8", "LC_ALL=C", "TMPDIR=/tmp/u", "PLAIN=hello", "AWS_PROFILE=x", "GITHUB_TOKEN=ghp_x"})
	if strings.Join(got, " ") != "PATH=/usr/bin HOME=/home/u LANG=en_US.UTF-8 LC_ALL=C TMPDIR=/tmp/u" {
		t.Errorf("Baseline = %q", got)
	}
	if !Switch([]string{AllowlistVar + "=on"}, AllowlistVar) || Switch([]string{AllowlistVar + "=0"}, AllowlistVar) {
		t.Errorf("the allowlist switch is not read as gnosh's switches are")
	}
}
