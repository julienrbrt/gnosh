package permission

import (
	"strings"
	"testing"
)

// treeDir is the work tree of these tests, made from the checkout /proj.
const treeDir = "/proj/.gnosh/worktrees/x"

// testTree stands for what the session tells from the disk: git opens the
// work tree's own repository in the work tree and nowhere else, and the
// repository has the remotes origin, fork and up.
var testTree = TreeGit{
	Repo: func(dir string) string {
		if dir == treeDir || strings.HasPrefix(dir, treeDir+"/") {
			return ""
		}
		return "git run in " + dir + " would open another repository than your work tree's own"
	},
	Remote:  func(name string) bool { return name == "origin" || name == "fork" || name == "up" },
	Stashes: func() bool { return false },
}

// personStashes is testTree with a stash of the person's in the list.
var personStashes = TreeGit{Repo: testTree.Repo, Remote: testTree.Remote, Stashes: func() bool { return true }}

// SharedGitChange: in a work tree of its own, git may read, or change the
// work tree's own branch, index and files in ways gnosh knows, in the work
// tree's own repository. Every line in the first list changes, or may
// change, what the work tree shares with the checkout; most were seen to
// on real git by the reviews of 17 November, which found the ways around
// the first two versions of the check.
func TestSharedGitChange(t *testing.T) {
	e := mustEngine(t, testOptions(ModeDefault, rules{}))
	for _, cmd := range []string{
		// Refs by name, and the subcommands that write them.
		"git update-ref refs/heads/main HEAD",
		"/usr/bin/git update-ref refs/heads/main HEAD",
		"git-update-ref refs/heads/main HEAD",
		"GIT update-ref refs/heads/main HEAD",
		"sudo git update-ref refs/heads/main HEAD",
		"sh -c 'git update-ref refs/heads/main HEAD'",
		"git symbolic-ref HEAD refs/heads/main",
		"git fast-import --force",
		"git send-pack --force . HEAD~2:refs/heads/release",
		"git replace a b",
		"git notes add -m x",
		"git reflog expire --expire=now --all",
		"git filter-branch --all",
		"git --shallow-file x update-ref refs/heads/release HEAD",
		// Branches, tags, stashes.
		"ls && git branch -D x",
		"echo x | xargs git branch -D",
		"echo -D release | xargs git branch",
		"git branch $(printf -- -D) release",
		"git branch -f main HEAD",
		"git branch -vD old",
		"git branch -m main trunk",
		"git branch --delete old",
		"git branch -u release main",
		"git branch --edit-description",
		"touch ./-D && git branch -? release",
		"touch ./-D && git branch ''?D release",
		"git checkout -B main",
		"git checkout --ignore-other-worktrees main",
		"git switch -C main",
		"git switch --force-create main",
		"git switch --ignore-other-worktrees main",
		"git tag -d v1",
		"git tag -f v1",
		"git stash clear",
		"git stash drop stash@{1}",
		"echo clear | xargs git stash",
		// The list of work trees.
		"git worktree remove ../x",
		"git worktree prune",
		"git worktree add -B main ../x",
		"git worktree $(echo remove) ../x",
		// Another repository than the work tree's own.
		"git -C /tmp/other branch -D x",
		"git -C /tmp/a update-ref refs/heads/release HEAD~2",
		"git -C /tmp init -q q && git -C /tmp/q push /proj :refs/heads/release",
		"git -C /tmp clone -q --no-local -u 'git -C /proj branch -D release;:' /proj c",
		"git --work-tree=/tmp update-ref refs/heads/release HEAD~2",
		"git --work-tree /tmp branch -D release",
		"git --git-dir=/proj/.git log",
		"cd /proj && git status",
		// Pushes and fetches into the repository itself.
		"git push . HEAD:main",
		"git push /repo HEAD:main",
		"git push ../repo HEAD:main",
		"git push src/.. HEAD:refs/heads/release",
		"git push src/../../../.. :refs/tags/v1.0",
		"git push lnk HEAD:refs/heads/release",
		"git push --repo=origin . HEAD:release",
		"git push --rece git-receive-pack . HEAD:release",
		"git push --receive-pack=x origin",
		"git push ''?-receive-pack* . HEAD",
		"git push $TO HEAD:main",
		"git push origin $BR",
		"git push -u fork main",
		"git push --all -u origin",
		"git fetch origin main:main",
		"git fetch . HEAD:refs/heads/main",
		"git pull origin +main:main",
		"git fetch origin $SPEC",
		"git fetch --stdin . <<< '+main:release'",
		"git fetch --refmap=+refs/heads/main:refs/heads/release . main",
		"git branch t@x main && git fetch . +t@x:release",
		"git fetch -u --stdin . <<< x",
		"git fetch --upload-pack=x origin",
		"git fetch -f origin tag v1",
		"git fetch --prune-tags origin",
		"git fetch --filter=blob:none origin",
		"git fetch --depth=1 origin",
		"git pull --depth=1",
		// Commands git runs.
		"git rebase --update-refs main",
		"git rebase --exec 'git branch -D release' HEAD~1",
		"git rebase -x true HEAD~1",
		"git rebase -i HEAD~3",
		"git rebase main feature",
		"git rebase --root -f release",
		"git rebase --onto=main --root release",
		"git merge -s mine feature",
		"git bisect run sh -c x",
		"git submodule foreach x",
		"EDITOR='git branch -D release' git commit --allow-empty",
		"GIT_SSH_COMMAND=x git fetch origin",
		"git grep -O'sh -c x' foo",
		"git grep -nO'git update-ref refs/heads/release HEAD~2;:' c",
		"git grep --open='git branch -D release;:' c",
		"git log --output=/proj/.git/config",
		"git log --out=/proj/.git/config",
		"git diff $(cat refs.txt)",
		// Settings, hooks and aliases.
		"git config core.fsmonitor x",
		"git config --global alias.zz update-ref",
		"git config --global core.hooksPath /tmp/h",
		"git config --local core.hooksPath /tmp/h",
		"git config --unset core.x",
		"git config set core.x y",
		"git config -e",
		`git config user.email "$E"`,
		"git config -f /repo/.git/config core.hooksPath x",
		"git remote set-url origin x",
		"git remote add up https://example.com/x.git",
		"git init --template=/tmp/t",
		"git init",
		"git maintenance register",
		"git -c alias.x=update-ref x refs/heads/main HEAD",
		"git -c 'alias.x=!sh' x",
		"git -c remote.origin.url=. push origin HEAD:release",
		"git -c 'remote.origin.fetch=+refs/heads/*:refs/heads/*' fetch -u origin",
		"git -c rebase.updateRefs=true rebase main",
		"git -c help.autocorrect=immediate updat-ref refs/heads/release HEAD",
		"git -c include.path=/tmp/a.cfg zz",
		"git -c core.editor='git branch -D release;:' commit --allow-empty",
		"git -c core.fsmonitor=x status",
		"git br -D release",
		"git brnach -D x",
		// The environment, set every way a shell has.
		"GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.x GIT_CONFIG_VALUE_0=update-ref git x refs/heads/main HEAD",
		"export GIT_CONFI${x}G_COUNT=1; git status",
		"HOME=/tmp/h git zz refs/heads/release HEAD",
		"PATH=/tmp/evil:$PATH git status",
		"time GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=remote.origin.pushurl GIT_CONFIG_VALUE_0=/proj git push origin HEAD:refs/heads/release",
		"set -a; : ${GIT_CONFIG_COUNT:=1} ${GIT_CONFIG_KEY_0:=remote.origin.pushurl} ${GIT_CONFIG_VALUE_0:=/proj}; git push origin HEAD:refs/heads/release",
		"printf -vGIT_CONFIG_COUNT 1; git push origin HEAD",
		"for HOME in /tmp/h; do git push origin HEAD:refs/heads/release; done",
		"printf -vHOME /tmp/h; git push origin HEAD",
		"declare -n r=HOME; r=/tmp/h; git push origin HEAD",
		"sudo HOME=/tmp/h git push origin HEAD",
		"sudo GIT_CONFIG_COUNT=1 git push origin HEAD",
		"read HOME < /tmp/h; git push origin HEAD",
		// Names only known when the line runs, or by a pattern.
		"git $SUB refs/heads/main HEAD",
		"/usr/bin/gi[t] update-ref refs/heads/release HEAD",
		"git update-re[f] refs/heads/release HEAD",
	} {
		if got := e.SharedGitChange(treeDir, cmd, testTree); got == "" {
			t.Errorf("SharedGitChange(%q) = nothing, want why not", cmd)
		}
	}
	for _, cmd := range []string{
		"git status",
		"git status --short",
		"git log --oneline -5",
		"git log --graph --since=2.weeks",
		"git log --format='%h %s' | head",
		"git diff HEAD",
		"git diff --stat",
		`git diff "$(git merge-base HEAD main)"`,
		`git log --oneline "$(git merge-base HEAD origin/main)"..HEAD`,
		`git show "$SHA"`,
		`git rev-parse "$REF"`,
		"git show HEAD:main.go",
		"git blame x.go",
		"git rev-parse HEAD",
		"git ls-files",
		"git grep -n foo",
		"git describe --tags",
		"git help update-ref",
		"git --version",
		"git -C sub status",
		"git add .",
		"git add -A",
		`git add -- "$f"`,
		"git add src/*.go",
		"git commit -m 'fix the bug'",
		"git commit -am 'x'",
		"git commit -m \"$(cat <<'EOF'\nfix the bug\nEOF\n)\"",
		"git commit --amend --no-edit",
		"git commit -F - <<'EOF'\nfix the bug\nEOF",
		"git -c user.name=x -c user.email=y commit -m z",
		`git -c "user.name=$N" commit -m x`,
		"git reset --hard HEAD~1",
		"git reset --hard @{u}",
		"git restore --staged x.go",
		"git rm --cached x",
		"git mv a b",
		"git checkout -b new",
		"git checkout main.go",
		"git checkout -- a.go",
		"git checkout -f",
		"git checkout HEAD~2",
		"git checkout @{-1}",
		"git switch -c new",
		"git switch main",
		"git switch --detach HEAD~1",
		"git merge feature",
		"git merge --no-ff -m x feature",
		"git rebase main",
		"git rebase @{u}",
		"git rebase --continue",
		"GIT_EDITOR=true git rebase --continue",
		"git cherry-pick abc123",
		"git cherry-pick -s abc123",
		"git revert --no-edit HEAD",
		"git revert -s HEAD",
		"git stash",
		"git stash push -m wip",
		"git stash apply",
		"git stash apply stash@{0}",
		"git stash list",
		"git stash show -p",
		"git stash show --stat",
		"git fetch origin",
		"git fetch --all --prune",
		"git fetch origin main",
		"git fetch origin +refs/heads/*:refs/remotes/origin/*",
		"git fetch --jobs 4 git@github.com:x/y.git main",
		"GIT_TERMINAL_PROMPT=0 git fetch origin",
		"git pull --rebase origin main",
		"git pull -r",
		"git pull",
		"git push origin HEAD",
		"git push -u origin HEAD",
		"git push --force origin x",
		"git push fork feature",
		"git push git@github.com:x/y.git HEAD",
		"git push https://example.com/x.git HEAD",
		`git push -- origin "$BR"`,
		"git branch",
		"git branch -a",
		"git branch -vv",
		"git branch new-thing",
		"git branch --sort=-committerdate",
		"git branch --format='%(refname)'",
		"git branch --show-current",
		"git branch --merged",
		"git branch --no-merged",
		"git tag v1",
		"git tag -a v1 -m 'release'",
		"git tag -l",
		"git tag --contains",
		"git symbolic-ref --short HEAD",
		"git symbolic-ref refs/remotes/origin/HEAD",
		"git ls-remote --heads origin",
		"git clean -fd",
		"git apply x.patch",
		"git bisect start",
		"git bisect good",
		"git submodule status",
		"git format-patch -1",
		"git config user.name",
		"git config --get-regexp '^core\\\\.'",
		"git config --global --get user.email",
		"git config get core.editor",
		"git config -l",
		"git remote -v",
		"git remote get-url origin",
		"git reflog",
		"git reflog show main",
		"grep -r GIT_CONFIG_PARAMETERS . && git status",
		"echo $HOME && git status",
		// Seen in the AI boxes' sessions (the replay of 17 November).
		"[ -d .git ] && git status --short",
		"git grep -n quokka $(git rev-list --all)",
		"git diff $(git merge-base HEAD main)",
		"git stash && go test ./... 2>&1; git stash pop",
		"git stash drop",
		"echo update-ref",
		"$G update-ref refs/heads/main HEAD", // a name only known when it runs: GitPlaces's to refuse
	} {
		if got := e.SharedGitChange(treeDir, cmd, testTree); got != "" {
			t.Errorf("SharedGitChange(%q) = %q, want nothing", cmd, got)
		}
	}
}

// With a stash of the person's in the list, which the checkout keeps too,
// a stash popped or dropped may be theirs.
func TestSharedGitChangeStashes(t *testing.T) {
	e := mustEngine(t, testOptions(ModeDefault, rules{}))
	for _, cmd := range []string{"git stash pop", "git stash drop", "git stash && go test ./...; git stash pop"} {
		if got := e.SharedGitChange(treeDir, cmd, personStashes); got == "" {
			t.Errorf("SharedGitChange(%q) with the person's stashes = nothing, want why not", cmd)
		}
	}
}

// On a disk that does not tell capital letters from small ones, a branch
// or a tag named in capitals may be one the checkout uses (MAIN is main),
// whether it is made or switched to.
func TestSharedGitChangeCase(t *testing.T) {
	was := foldsCase
	foldsCase = true
	defer func() { foldsCase = was }()
	e := mustEngine(t, testOptions(ModeDefault, rules{}))
	for cmd, refused := range map[string]bool{
		"git branch MAIN main~2":         true,
		"git branch -v MAIN main~2":      true,
		"git switch -c RELEASE main~2":   true,
		"git checkout -b Fix-1":          true,
		"git switch MAIN":                true,
		"git checkout Main":              true,
		"git checkout README.md":         true,
		"git tag V1.0 HEAD":              true,
		"git checkout -- README.md":      false,
		"git branch fix-1":               false,
		"git switch -c release-2 main~2": false,
		"git switch main":                false,
		"git tag v1.0":                   false,
	} {
		if got := e.SharedGitChange(treeDir, cmd, testTree); (got != "") != refused {
			t.Errorf("SharedGitChange(%q) = %q, want refused %v", cmd, got, refused)
		}
	}
}

// SharedGitChange reads any line without failing, however its git words
// are run together or cut short.
func FuzzSharedGitChange(f *testing.F) {
	for _, s := range []string{
		"git branch -vD x", "git -c alias.x=y x", "git fetch --depth 1 o a:b",
		"git config -f x a b", "git push --repo=. x", "git-update-ref a",
		"git checkout -B", "git switch --force-create", "git worktree add -", "git tag -", "git -c",
		"git commit -m", "git -C", "git push -- origin", "GIT_EDITOR=true git rebase --continue",
		"git push -u", "git branch --merged", "git format-patch -", "for HOME in", "printf -v",
	} {
		f.Add(s)
	}
	e := mustEngine(f, testOptions(ModeDefault, rules{}))
	f.Fuzz(func(t *testing.T, line string) {
		e.SharedGitChange(treeDir, line, testTree)
	})
}
