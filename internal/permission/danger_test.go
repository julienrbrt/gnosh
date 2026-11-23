package permission

import (
	"slices"
	"testing"
)

func TestDangerousRemovals(t *testing.T) {
	// Every Bash command is allowed, and there are no deny rules, so the
	// only thing that can stop these is the removal check itself.
	o := testOptions(ModeDefault, rules{allow: []string{"Bash"}})
	o.Layers = append(o.Layers, Layer{Scope: ScopeCLI, Name: "--add-dir", Dirs: []string{"/data/extra"}})
	e := mustEngine(t, o)

	dangerous := []string{
		// The root and its direct children.
		"rm -rf /", "rm -rf /*", "rm -rf /usr", "rm -rf /etc/", "rm -r /var", "rmdir /opt", "rm -rf //",
		"rm -rf /usr/../etc", "rm -rf --no-preserve-root /", "rm -rf -- /", "rm -fr /home",
		// The home directory.
		"rm -rf ~", "rm -rf ~/", "rm -rf ~/*", "rm -rf $HOME", "rm -rf \"$HOME\"", "rm -rf ${HOME}", "rm -rf $HOME/*",
		"rm -rf /home/u", "rm -rf /home/u/",
		// The working directory and what is above it.
		"rm -rf .", "rm -rf ./", "rm -rf ..", "rm -rf /proj", "rm -rf *", "rm -rf ./*", "rm -rf .*", "rm -rf /proj/*",
		"rm -rf $PWD", "rm -rf \"$PWD\"/*", "cd src && rm -rf ..", "rm -rf src/../..",
		// An additional directory or its parents, swept by a pattern.
		"rm -rf /data/extra/*", "rm -rf /data/*",
		// A pattern or trailing slash right under a variable.
		"rm -rf \"$DIR\"/*", "rm -rf $DIR/*", "rm -rf ${DIR}/*", "rm -rf \"$DIR\"/", "rm -rf $DIR/", "rm -rf $DIR/*/",
		"rm -rf \"${DIR:-}\"/*", "rm -rf $A$B/*", "rm -rf \"$DIR/sub\"/*",
		// A variable followed by one common top-level name.
		"rm -rf \"$TMPDIR/mnt\"", "rm -rf $ROOT/usr", "rm -rf $X/etc/",
		// A variable set in the same line from a command's output.
		"D=$(pwd); rm -rf \"$D\"", "D=`pwd` && rm -rf $D", "D=$(dirname x); rm -r $D",
		// A recursive removal of whatever a command prints.
		"rm -rf \"$(pwd)\"", "rm -rf $(pwd)", "rm -rf `pwd`", "rm -rf \"$(git rev-parse --show-toplevel)\"",
		"rm -rf $(pwd)/*", "rm -rf \"$(pwd)\"/*/",
		// The check looks inside.
		"(rm -rf /)", "{ rm -rf ~; }", "echo $(rm -rf /)", "echo `rm -rf ~`", "cat <(rm -rf /)",
		"sh -c 'rm -rf /'", "bash -c \"rm -rf ~\"", "sudo rm -rf /", "env rm -rf /", "timeout 5 rm -rf ~",
		"xargs rm -rf /", "if true; then rm -rf /; fi", "for d in a; do rm -rf ~; done", "eval 'rm -rf /'",
		"ls && rm -rf / || true", "find . -exec rm -rf / \\;", "/bin/rm -rf /", "nohup rm -rf ~ &",
		// A removal in a line the reader cannot follow is not ruled out.
		"rm -rf $x &&", "select f in a b; do rm -rf $f; done", "rm -rf ${x:-\"}\"}", "echo 'x; rm -rf y",
		// A program's name with a variable in it, read with the variable
		// unset (oracle seed 126).
		"rmdir${X[1]} . -n", "rmdir${X} .", "r${Y}m -rf /", "rm${X[1]} -rf ~",
		// A word that starts with = is only zsh's way of naming a program:
		// to bash and sh it is text (oracle seed 135).
		"rm -rf =x/..", "rm -R ='*/..'", "rm -rf =/..", "rm -rf =/../..", "w=false'*/..'; r\\m -R ='*/..'",
		// A program that a flag of its own sends to another folder runs
		// there, and so does a command fc runs again from what history -s
		// put in the shell's history.
		"env --chdir=/ rm -rf usr", "env -C / rm -rf etc", "env -iC/ rm -rf usr", "env --ch=/ rm -rf usr", "sudo -D / rm -rf usr",
		"env -C ~ rm -rf .", "env -C \"$D\" rm -rf .", "set -o history\nhistory -s 'rm -rf ~'\nfc -s",
		"git -c alias.z='!rm -rf ~' z", "git -c core.sshCommand='rm -rf ~' fetch", "export PAGER='rm -rf ~'; git -p log",
		"echo 'rm -rf ~' | /proc/$$/exe", "echo 'rm -rf ~' | /proc/self$x/exe",
	}
	for _, cmd := range dangerous {
		d := bash(e, cmd)
		if d.Behavior != Ask || !d.Critical || d.Stage != StageSafety || len(d.Suggestions) != 0 {
			t.Errorf("%q: got %s critical=%v at %s (%s), want a critical ask", cmd, d.Behavior, d.Critical, d.Stage, d.Reason)
		}
		if h := d.WithHook(HookAllow, ""); h.Behavior != Ask {
			t.Errorf("%q: a hook approved a dangerous removal", cmd)
		}
	}

	safe := []string{
		"rm a.txt", "rm -rf build", "rm -rf ./build", "rm -rf /proj/build", "rm -rf src/gen", "rm *.tmp", "rm -rf build/*",
		"rm -rf /usr/local/x", "rm -rf /tmp/x", "rm -rf ~/x", "rm -rf $HOME/x", "rm -rf ~/.cache/thing",
		"rm -rf /data/extra/sub", "rm -rf /data/extra/sub/*", "rmdir empty",
		"rm -rf \"${DIR:?}\"/*", "rm -rf \"${DIR:?must be set}/\"", "rm -rf $DIR", "rm -rf \"$DIR/build\"", "rm -rf $BUILD/out",
		"rm \"$(pwd)/a.txt\"", "rm $(ls *.tmp)", "D=x; rm -rf \"$D\"", "echo rm -rf /", "echo 'rm -rf /'", "grep -r 'rm -rf /' .",
		"ls /", "rm -- -rf", "git rm -r --cached .", "rm -rf =old", "rm -rf =x/build", "env -C build rm -rf out", "history -s ls", "git -c core.pager=less log", "git -c alias.st=status st", "git config core.editor vim",
		// Forms the reader follows are judged on what they remove.
		"[[ -d build ]] && rm -rf build", "rm -rf \"${dirs[@]}\"", "for ((i=0;i<3;i++)); do rm -f tmp$i; done",
		"(( n > 0 )) && rm -rf build",
	}
	for _, cmd := range safe {
		if d := bash(e, cmd); d.Critical {
			t.Errorf("%q: flagged as a dangerous removal (%s)", cmd, d.Reason)
		}
	}

	// The per-mode table of section 10.
	for mode, want := range map[Mode]Behavior{
		ModeDefault: Ask, ModeAcceptEdits: Ask, ModePlan: Ask, ModeDontAsk: Deny, ModeBypass: Ask,
	} {
		if err := e.SetMode(mode); err != nil {
			t.Fatal(err)
		}
		d := bash(e, "rm -rf /")
		if d.Behavior != want || !d.Critical {
			t.Errorf("rm -rf / in %s: got %s critical=%v, want %s", mode, d.Behavior, d.Critical, want)
		}
	}
	// A deny rule still comes first.
	e = engine(t, ModeBypass, rules{deny: []string{"Bash(rm *)"}})
	if d := bash(e, "rm -rf /"); d.Behavior != Deny || d.Stage != StageDeny {
		t.Errorf("deny before the removal check: %+v", d)
	}
	// The shell's current directory counts as well as the primary one.
	e = engine(t, ModeBypass, rules{})
	e.SetCwd("/proj/src/deep")
	for _, cmd := range []string{"rm -rf .", "rm -rf ..", "rm -rf ../..", "rm -rf /proj/src"} {
		if d := bash(e, cmd); !d.Critical {
			t.Errorf("%q from /proj/src/deep: not flagged", cmd)
		}
	}
	if d := bash(e, "rm -rf ../other"); d.Critical {
		t.Errorf("a sibling directory was flagged: %s", d.Reason)
	}
}

// TestOtherDestroyers: the other commands that destroy a directory which
// has to be kept are asked about like rm, in every mode, and no rule or
// hook approves one; their ordinary uses run as before.
func TestOtherDestroyers(t *testing.T) {
	o := testOptions(ModeBypass, rules{})
	o.Protected = []string{"/home/u/.config/gnosh", "/home/u/.local/share/gnosh"}
	e := mustEngine(t, o)
	destroys := []string{
		// mv takes it away.
		"mv ~ /tmp/h", "mv .git /tmp/g", "mv .git .git.bak", "mv * /tmp/", "mv ./* ../elsewhere", "mv -t /tmp .git src", "mv -t /tmp *",
		"mv --target-directory=/tmp *", "mv -vt /tmp .git", "mv /proj /tmp/p", "mv . /tmp/p", "mv ~/.config/gnosh /tmp/c", "mv ~/* /tmp/",
		"mv /home /x", "sudo mv /etc /x", "mv ${HOME} /tmp/h", "d=.git; mv $d /tmp", "mv ../proj /tmp", "mv \"$X\"/* /tmp", "mv $X/ /tmp",
		"mv -T /tmp/new ~", "mv --no-target-directory /tmp/new .git", "for d in .git src; do mv $d /tmp/; done", "mv \"$(pwd)\" /tmp/x",
		"mv ~/.local/share/gnosh /tmp", "mv ~/.config /tmp", "mv .gi* /tmp", "mv {.git,x} /tmp",
		// A $"..." is a catalog's translation, which nobody knows ahead of
		// time; and in a line that holds one, a process substitution is
		// still a command's output (oracle seed 128).
		"mv $\"x\" <(env) link-dir/file >& all.txt", "mv $\"x\" /tmp", "chmod -R 000 $\"x\"",
		// In dash an alias given $'1rm' is $1rm, which is rm (oracle seed
		// 131).
		"alias zz=$'1rm'\nzz -rf ~", "sh -c $'1rm -rf ~'", "trap $'1rm -rf ~' EXIT",
		// A variable inside a name right under the root leaves that name
		// where it is empty (oracle seed 105): /tmp-a, /tmp:--rf.
		"mv /tmp$X-a /tmp", "mv /tmp$X:--rf /tmp", "mv $X:--rf x} /tmp$X:--rf x} /tmp", "mv /tm${X}p /x",
		// The same through a variable made of it.
		"t=/tmp$X-a; mv $t /tmp", "t=/tm${X}p; rm -rf $t",
		// A default written with blanks is cut into words where its
		// variable is not set: this is mv .git /tmp/g /tmp.
		"mv ${X:-.git /tmp/g} /tmp", "mv ${X:-.git /tmp/g}\\ /tmp", "mv ${X:-.git /tmp/g}", "mv ${X:-.git /tmp/g}''", "mv ${HOME:+.git /tmp/g}",
		"chmod -R ${X:-000 .}", "rsync -a --delete ${X:-src/ .}", "git ${X:-clean -fdx}", "sudo mv ${X:-.git /tmp/g} /tmp",
		// What a variable holds is cut into words where it stands outside
		// double quotes, whatever is quoted beside it.
		"x='.git /tmp/g'; mv $x\\ /tmp", "x='.git /tmp/g'; mv ${x}'' /tmp", "x='.git /tmp/g'; mv \"\"$x", "x='-R 000 .'; chmod $x''",
		"x='src/ .'; rsync -a --delete $x''", "x='clean -fdx'; git $x''",
		// One word that the shell writes out as all of mv's operands: in
		// dash ".*" is ".", ".." and every name that starts with a dot.
		"mv .*", "mv *", "mv ./*", "mv {.git,/tmp/g}", "''mv .*", "while mv .*; do x; done", "chmod -R {000,.}",
		"rsync -a --delete {src/,.}",
		// chmod, chown and chgrp over all of it.
		"chmod -R 000 .", "chmod -R 777 ~", "chmod -R a-w /", "chmod -R -w .", "chown -R nobody /", "chown -R u:g ~/",
		"chown -R --reference=a.txt .", "chgrp -R x ..", "chmod --recursive 700 .git", "chmod -fR 000 *", "chown -R u \"$X\"/",
		"chown -Rv u /etc", "chmod -R 000 ~/.config/gnosh", "chmod -R u+w ./*", "chown -hR u /usr", "chmod -R 644 $PWD",
		// git clean of what was never committed.
		"git clean -fdx", "git clean -xdf", "git clean -f -d -x", "git clean -fdX", "git clean --force -dx", "git -C .. clean -fdx",
		"git -C ~ clean -fdx", "git clean -fdx .", "git clean -fdx ~", "git -c core.x=y clean -ffdx", "cd ~ && git clean -fdx",
		"git --no-pager clean -xfd", "git clean -fdx -e keep", "git clean -fdx -- .",
		// dd, truncate and shred onto it.
		"dd if=/dev/zero of=/dev/sda", "dd of=/dev/nvme0n1 if=x", "shred -u *", "shred -u ~/*", "truncate -s 0 *", "truncate -s0 ./*",
		"shred /dev/sda1", "truncate -s 0 .git/*", "shred -u \"$DIR\"/*", "truncate --size=0 ~/.config/gnosh/*", "dd if=x of=/dev/disk2",
		"shred -n 3 -u /proj/*",
		// An empty variable before "/." leaves the root, and shred has no
		// -r to take the word after it (oracle seed 116).
		"shred -rf $d/.", "shred $d/.", "truncate -s 0 $d/.", "rm -rf $d/.",
		// rsync that deletes what is in the way, or takes its sources away.
		"rsync -a --delete src/ .", "rsync -a --delete empty/ ~/", "rsync --del a/ /", "rsync -av --delete-after src/ ..",
		"rsync -a --delete x/ ~/.config/gnosh", "rsync -a --delete -e ssh host:dir/ .", "rsync -a --remove-source-files * host:/backup/",
		"rsync -a --remove-source-files ~ host:x", "rsync -a --delete src/ \"$DEST\"/", "rsync -a --delete --exclude .git src/ ./",
		"rsync --delete-excluded -a a/ /proj",
		// find that shreds what it finds.
		"find . -exec shred -u {} +", "find ~ -delete",
		// The folders that are kept, by rm as well.
		"rm -rf .git", "rm -rf ./.git/", "rm -rf .git/*", "rm -rf ../proj/.git", "rm -rf ~/.config/gnosh", "rm -rf ~/.config",
		"rm -rf ~/.local/share/gnosh/*", "rm -rf ~/.local", "rmdir .git", "rm -rf .gi*", "rm -rf .[!.]*", "rm -rf /proj/.GIT",
		// What the second round's long runs found (seeds 48 to 50), and what
		// reading on from there turned up.
		// Every word of an array at once, as arguments and as the command.
		"a=(-R 000 .); chmod \"${a[@]}\"", "a=(000 .); chmod -R \"${a[@]}\"", "a=(clean -fdx); git \"${a[@]}\"", "a=(.git /tmp/g); mv \"${a[@]}\"",
		"a=(-fdx); git clean ${a[*]}", "a=(/tmp/g .git /tmp/g); mv \"${a[@]}\"", "d=.git; w=(.git $d); d=.git; mv \"${w[@]}\" /tmp/g", "a=(x); a+=(.git /tmp/g); mv ${a[*]}[*]}", "a=(x .git /tmp/g); mv \"${a[@]}\".bak", "a=(.git /tmp/g); mv pre\"${a[@]}\"", "a=(.git /tmp/g); mv ${a[@]}*", "a=(clean -fdx); git ${a[@]}*", "sh -c 'git \"$@\"' sh clean -fdx", "f() { git \"$@\"; }; f status; f clean -fdx", "a=(status); a+=(clean -fdx); git \"${a[@]}\"",
		"set -- clean -fdx; git \"$@\"", "f() { git $1 $2 $3; }; f clean -fdx", "f() { mv $1 $2; }; f /tmp/g .git", "f() { shift; git \"$@\"; }; f x clean -fdx", "bash -c 'git \"$@\"' _ -C .. clean -fdx", "a=(-R 000 .); chmod \"${a[@]}\"/", "a=(.git /tmp/g); mv x\"${a[@]}\"",
		"a=(mv .git /tmp/g); ${a[@]}", "a=(rm -rf /); \"${a[@]}\"", "a=(x); a=(rm -rf ~); ${a[@]}", "a=(chmod -R 000 .); ${a[*]}",
		// The POSIX forms on every word of an array (seed 105), and bash's.
		"a=(-R 000 . -R 000 .); chmod \"${a[@]=}\"", "a=(-R 000 .); chmod \"${a[@]-}\"", "a=(-R 000 .); chmod \"${a[@]:-x}\"", "a=(-R 000 .); chmod ${a[*]:=}",
		"a=(-R 000 .); chmod \"${a[@]?}\"", "a=(000 .x); chmod -R \"${a[@]%x}\"", "a=(000 .x); chmod -R \"${a[@]/x}\"", "a=(-R 000 .); \"${a[@]:+chmod}\" \"${a[@]}\"",
		"a=(x); chmod -R 000 \"${a[@]:+.}\"", "a=(mv .git /tmp/g); \"${a[@]-}\"", "a=(CLEAN -FDX); git \"${a[@],,}\"", "chmod -R 000 \"${n[@]:-.}\"",
		// A pattern of plain text is cut off exactly (seed 106).
		"a=(-a --delete src/ .x); rsync \"${a[@]%x}\"", "a=(x-a x--delete xsrc/ x.); rsync \"${a[@]#x}\"", "d=.x; rsync -a --delete src/ \"${d%x}\"", "d=./; rm -rf ${d%%/}",
		// A value made of a variable that was set for another command only.
		"v=-u *; w=./$v; shred $w", "v=u *;w=.$v;shred $w",
		// To a plain sh a {name} before a redirection is an argument: the
		// place the directory is moved to.
		"mv . {fd}>log", "mv .git {H}>1", "sudo mv . {fd}>&2", "mv ~ {fd}>/dev/null",
		// What xargs adds is as many words as it read, and so is what a
		// command prints when it is all the command is handed.
		"echo .git /tmp/g | xargs mv", "echo 000 . | xargs chmod -R", "echo src/ . | xargs rsync -a --delete", "echo /tmp . | xargs mv -t",
		"echo . | xargs chmod -R 000", "echo . | xargs git clean -fdx", "ls | xargs shred -u", "ls | xargs truncate -s 0", "echo x | xargs mv a.txt",
		"echo . | xargs -I{} mv {} /tmp/x", "mv $(cat list)", "chmod -R $(cat args)", "rsync -a --delete $(cat args)", "echo x | xargs timeout 5 git clean -fdx",
		// The words after an alias where it is used, and after the command
		// a builtin calls.
		"alias zz='mv .git'\nzz /tmp/g", "alias r='rm -rf'\nr /", "alias zz=\nzz rm -rf /", "alias s='sudo '\nalias r='rm -rf'\ns r ~", "alias a=b\nalias b='rm -rf'\na /", "mapfile -t -C 'rm -rf' -c 1 a <<< /",
		"mapfile -t -C 'mv .git' -c 1 a <<< /tmp", "readarray -C 'chmod -R 000' -c 1 a <<< .", "compgen $x-C'mv EOF* /tmp/'", "mapfile $x-C'rm -rf' -c 1 a <<< /",
		"alias zz=${x}rm\nzz -rf /", "alias zz=$X'mv .git'\nzz /tmp/g",
		// A name the shell's own tables make another program, with the
		// words that follow it.
		"hash -p /bin/rm ls; ls -rf /", "BASH_CMDS[ls]=/bin/rm; ls -rf /", "shopt -s expand_aliases; BASH_ALIASES[zz]='rm -rf'\nzz /", "ls() { rm \"$@\"; }; ls -rf /",
		"hash -p /bin/mv ls; ls .git /tmp/g",
		// A variable that is a name for another one.
		"d=.ssh; declare -n d=w; w=$d/; truncate -s 0 $w", "d=build; declare -n r=d; r=/; rm -rf $d", "d=build; declare -n r; r=d; r=~; rm -rf $d",
		"d=a.txt; declare -n d=w; w=$d; rm -rf ./$d", "declare -n d; d=PWD; w=$d; chmod -R 000 $w", "declare -n d=HOME; rm -rf $d", "declare -n d=PWD; mv $d /tmp/x", "d=build; typeset -n r=d; r=.; chmod -R 000 $d", "d=build; declare -gn r=d; r=..; mv $d /tmp/x",
		// One of the line's variables beside one nobody set, and a variable
		// nobody set in front of a directory that has to be kept.
		"d=~; ((d++)); w=${X}$d; rm -rf $w", "d=~; rm -rf $PREFIX$d", "rm -rf $X/home/u", "rm -rf ${X}$HOME", "d=.git; mv $X$d /tmp/g",
		"rm -rf $X/home/u/.config/gnosh", "d=.; chmod -R 000 $X$d", "d=.; w=$d* && rm -rf $X$w", "w='*'; rm -rf $X$w", "d=~; rm -rf $X$d*", "cd src && d=~ && w=$d* && rm -rf $X$w", "rm -rf $X/home/u*", "rm -rf $PREFIX/ho*/u", "rm -rf $X/b*", "rm -rf $X/pr*", "d=..; rm -rf \"$DIR\"/$d", "d=build; read d; rm -rf $ROOT/$d", "rm -rf $X/home/u/.config",
		// A value of the line's own in the place git is sent to.
		"f() { d=x; }; w=~/$d; f; git -C $w clean -fdx", "d=x; read d </dev/null; w=./$d; git -C $w clean -fdx", "(d=a.txt); w=./$d; git -C $w clean -fdx",
		"d=src | cat; w=../$d; git -C $w clean -fdx", "git -C \"$(git rev-parse --show-toplevel)\" clean -fdx", "git -C \"$X\"/ clean -fdx",
		// Options written short, and a clean that is not told to force.
		"chmod --rec 000 .", "chown --recursiv u .", "chown -R --ref=a.txt .", "mv --t=/tmp .git", "mv --no-t /tmp/x .git", "mv --target /tmp .git",
		"mv --target-dir /tmp ~", "git clean --forc -dx", "git -c clean.requireForce=false clean -dx", "git clean -dx", "git clean -X", "git clean -xi",
		// find that hands everything it finds to another destroyer.
		"find . -exec mv -t /tmp {} +", "find . -maxdepth 1 -exec mv {} /tmp \\;", "find . -exec chmod -R 000 {} +", "find ~ -exec truncate -s 0 {} +",
		"find . -exec chown u {} +", "find .. -execdir mv {} /tmp \\;",
		// A line the reader cannot follow, judged by its pieces.
		"coproc mv .git /tmp/g", "coproc git clean -fdx", "select x in a; do chmod -R 000 .; break; done", "chmod -R 00$[0 .", "echo $[1+1]; mv * /tmp",
		// In a line that cannot be read, a word before the command's name
		// may stand for nothing: another piece of the line made it an
		// alias, which dash expands in the lines after.
		"alias zz=\nhead -5 a.txt$[; zz rsync -a --delete src/ .", "alias zz=\necho $[; zz mv .git /tmp/g", "alias q=\necho $[1; q chmod -R 000 .",
		"alias zz=\necho $[; zz git clean -fdx", "echo $[; zz shred -u *",
		// And the name itself may be written inside a ${...} or with quotes
		// in it, which a crude cut at blanks leaves as it is. (A descriptor
		// of three digits is one more thing the reader does not follow: to
		// dash the 000 is chmod's mode.)
		"env ${HOME:+chmod} -R 000> eval000 .", "echo $[; ${HOME:+mv} .git /tmp/g", "echo $[; ch''mod -R 000 .", "echo $[; X=1 \\mv .git /tmp/g",
		"echo $[; ${X:-rsync} -a --delete src/ .", "echo $[; \"git\" clean -fdx",
		// A ${...} in a line the reader cannot follow stays in its word
		// (oracle seed 100).
		"echo $((1)+(1)); rsync -a --${x}delete src/ .", "echo $((1)+(1)); chmod -R${x} 000 .",
		// A descriptor written with more than one digit is a word to dash
		// and a descriptor to bash: what each of them would run is judged
		// (oracle seed 100).
		"chmod -R 02>&1 .", "chmod${IFS}-R 02>&1 .", "mv 10>x .git /tmp/g", "rsync -a --delete src/ 10>&1 .",
		// A pattern or a list in braces that stands last stands for names
		// that are moved too: all of them but the last (oracle seed 100).
		"mv f /*", "mv a.txt {.git,/tmp/g}", "mv f ~/*",
		// A backslash at the end of a line in the middle of a word stops
		// the reader; the shells join the lines (oracle seed 101).
		"a[0]=rsync; $a -\\\na --delete src/ .", "x=chmod; $x -\\\nR 000 .",
		"time coproc shred -u *", "echo $[1]; rsync -a --delete src/ .", "mv ${HOME:0:${#HOME}} /tmp/h", "chmod -R 000 \"${PWD%\"x\"}\"",
		// A piece of a line the reader cannot follow keeps what it is
		// handed as it is written: <(date) is what a command prints, not
		// the word date (oracle seed 161).
		"coproc kill ;\nmv <(date) 0", "coproc kill; mv <(date -d yesterday ./src/../a.txt) 0", "coproc kill; mv $(tr a 'b c') <(date) 0",
		"coproc kill; while true; do mv <(date) 0; break; done", "coproc kill; x=$(mv <(date) 0)", "coproc kill; { mv <(date | cat) 0; }",
		"coproc kill; mv <(date) 0 2>&1", "coproc kill; (mv <(date) 0)", "coproc kill; \"mv\" <(date) 0", "coproc kill; mv <(date) 0 &>/dev/null",
		"while if whoami; then sort ../outside | alias; fi; do ! coproc kill ;\nmv ${X:-$(tr --complement ./src/../a.txt -n-f$X.env 'a b'${X})} <(date 010100002030 -d yesterday ./src/../a.txt) 0; break; done",
		// The forms bash adds to ${...}, on a value the line knows.
		"rm -rf ${HOME/x/y}", "rm -rf ${HOME:0:1}", "v=HOME; rm -rf ${!v}", "d=build; rm -rf ${d/build//}", "d=/x; rm -rf ${d:0:1}", "rm -rf ${PWD//zz/y}",
		"rm -rf ${HOME,,}", "d=/HOME/U; rm -rf ${d,,}", "mv ${HOME/x/y} /tmp/h", "d=.GIT; mv ${d,,} /tmp/g", "v=PWD; chmod -R 000 ${!v}", "d=HOME; d=y true; rm -rf ${!d}",
		"rmdir ${HOME, ,}", "rm -rf ${HOME@U}", "rm -rf ${HOME/#x/y}", "rm -rf ${PWD/*zz/y}", "d=.; rm -rf ${d@Q}", "mv ${HOME^h} /tmp/h",
		// A flag, or git's subcommand, with a variable written onto it.
		"sh -c 'git clean$x -fdx'", "git clean$x -fdx", "git $X clean -fdx", "git clean -fdx$x", "git ${X:-clean} -fdx", "rm -r$x .", "rm -rf$X /",
		"chmod -R$v 000 .", "mv -t$X/tmp .git", "mv --t=${X:-/tmp} .git", "rsync -a --delete$x src/ .", "chmod $x-R 000 .", "mv $x-t /tmp .git",
		"git $x-C .. clean -fdx", "rm ${X:--rf} .", "rm $x-rf ~",
		// git's subcommand built from what a command prints, or after a
		// printed word that may be nothing, with the flags written out.
		"git c`true`lean -fdx", "f=-c; sh $f 'git c`lean -it c`lean -fdx'", "git $(true) clean -fdx", "git cl$(x)ean -xdf", "git \"$(x)\" -fdx",
		// The words a function of the line's own, a set -- or a script
		// after sh -c is handed are what $1 and "$@" stand for in it.
		"f() { rm -rf \"$1\"; }; f ~", "clean() { rm -rf \"$1\"; }; clean build; clean .", "f() { mv \"$@\"; }; f .git /tmp/g", "set -- .git /tmp/g; mv \"$@\"",
		// A word with a variable nobody set in it, handed to a function, is
		// what is left of it when the variable is empty (oracle seed 99).
		"f() { mv \"$@\"; }; f .$git /tmp/g", "f() { mv \"$1\" /tmp/g; }; f .$git", "d=.; f() { rm -rf \"$1\"; }; f $d$X",
		"set -- /; rm -rf \"$1\"", "sh -c 'rm -rf \"$1\"' sh /", "bash -c 'rm -rf \"$0\"' ~", "bash -c 'mv \"$@\"' _ .git /tmp/g", "f() { \"$@\"; }; f rm -rf /",
		"function g { chmod -R 000 $1; }; g .", "f() { shift; rm -rf \"$1\"; }; f x ~", "set -- . /tmp/x; mv \"$@\"", "f() { git -C \"$1\" clean -fdx; }; f ..",
		// A brace that nothing closes is a character like any, and leaves
		// the first word alone (oracle seed 117).
		"f() { mv \"$@\"{; }; f .git /tmp/g", "set -- .git /tmp/g; mv \"$@\"{", "f() { rm -rf x{ \"$@\"; }; f .",
		// Braces beside a variable: bash takes them apart before it fills
		// the variable in, and dash leaves them as characters.
		"f() { mv \"$@\"{,x}; }; f .git /tmp/g", "sh -c 'mv \"$@\"{,x}' sh .git /tmp/g", "set -- .git /tmp/g; mv \"$@\"{,.bak}",
		// Braces beside a ${name}, and the positional parameters in braces
		// (the second review of 16 November).
		"rm -rf ${HOME}{,.old}", "rm -rf {${HOME},x}", "x=.git; mv ${x}{,.bak}", "f() { mv \"${1}\"{,x}; }; f .git", "f() { mv \"$1\"{,\\x}; }; f .git",
		"f() { mv \"${1}\" /tmp/g; }; f .git", "set -- .git; mv \"${1}\" /tmp/g", "f() { rm -rf \"${@}\"; }; f .", "f() { mv \"${1:-x}\" /tmp/g; }; f .git",
		// The program is one of more values than gnosh follows.
		"for t in a b c d e f g h i j k l m n o p q r; do $t x; done",
	}
	for _, cmd := range destroys {
		d := bash(e, cmd)
		if d.Behavior != Ask || !d.Critical || d.Stage != StageSafety || len(d.Suggestions) != 0 {
			t.Errorf("%q: got %s critical=%v at %s (%s), want a critical ask", cmd, d.Behavior, d.Critical, d.Stage, d.Reason)
		}
		if h := d.WithHook(HookAllow, ""); h.Behavior != Ask {
			t.Errorf("%q: a hook approved it", cmd)
		}
	}
	ordinary := []string{
		// $'...' as models write it: dash's reading of it holds nothing
		// that destroys.
		"grep -c $'\\t' a.txt", "rm $'old file.txt'", "printf $'%s\\n' a b", "IFS=$'\\n'; for f in a b; do echo $f; done",
		"mv a.txt b.txt", "mv build/out dist/", "mv *.log logs/", "mv src/a.go src/b.go", "mv x ~/", "mv x .", "mv -t build a.txt b.txt",
		"mv \"$tmp\" out.txt", "mv \"$f\" \"$f.bak\"", "mv vendor/x/.git /tmp/g", "mv -T /tmp/new build", "mv -f a b", "mv src/* dst/",
		"mv $tmp $dest", "mv -n a.txt ~/Documents/", "mv {a.txt,b.txt}", "mv logs/*.log", "chmod -R {755,build}",
		"mv a.txt {b,c}.txt", "mv x.txt *.bak", "ls 10>&1", "make build 77>&1",
		// A number the call hands a written word to is that word, not
		// nothing: the function removes proj/build, not /build.
		"f() { rm -rf \"$1/build\"; }; f proj", "f() { mv \"$1\" \"$2\"; }; f a.txt b.txt",
		"mv ${X:-a.txt b.txt}", "mv \"${X:-.git /tmp/g}\" /tmp", "chmod -R ${X:-755 build}", "mv ${X:-a b} dst/",
		"x='.git /tmp/g'; mv \"$x\" /tmp", "x='a.txt b.txt'; mv $x''", "x='755 build'; chmod -R $x''", "x='.git /tmp/g'; mv \"$x\"'' /tmp",
		// (A line that cannot be read, with such a command behind another
		// word and nothing that has to be kept in its way.)
		"alias zz=\necho $[; zz mv a.txt b.txt", "echo $[1; q chmod -R 755 build", "echo $[; zz rsync -a src/ dst/",
		"echo $[; ${X:-mv} a.txt b.txt", "echo $[; ch''mod -R 755 build", "env ${X:-chmod} -R 755> eval000 build",
		"chmod +x script.sh", "chmod 644 a.txt", "chmod -R u+w build", "chown -R me:me node_modules", "chmod 700 ~", "chmod -R 755 src",
		"chown u .", "chmod -R go-w \"$dir\"", "chmod -x a.sh", "chgrp staff a.txt", "chmod -R 755 ~/bin",
		"git clean -fd", "git clean -n -fdx", "git clean -fdx --dry-run", "git clean -fdx build", "git clean -fX build/", "git clean -f",
		"git status", "git -C build clean -fdx", "git clean -fdx \"$dir\"", "git clean -nfdx", "git commit -m 'clean -fdx'",
		"dd if=a of=b", "dd if=/dev/zero of=build/img bs=1M count=1", "truncate -s 0 build.log", "shred -u secret.key",
		"truncate -s 0 \"$f\"", "shred -u \"$tmp\"", "dd if=/dev/sda of=disk.img", "truncate -s 1G build/sparse",
		"rsync -a src/ build/", "rsync -a --delete src/ dist/", "rsync -a --delete src/ host:/srv/app/", "rsync -a . host:backup/",
		"rsync -a --delete src/ \"$dest\"", "rsync -a ~/notes/ backup/", "rsync -a --delete build/ ~/public_html/",
		"rm .git/index.lock", "rm -rf .git/hooks/x", "rm -rf vendor/x/.git", "rm ~/.config/gnosh/old.json", "rm -rf ~/.cache",
		"rm -rf node_modules", "rm -rf ~/.config/other", "rm -rf ~/.local/share/other",
		"find . -name '*.orig' -exec shred -u {} +",
		// The ordinary kin of what the second round closed.
		"mv a.txt $(mktemp -d)", "mv a.txt \"$(mktemp -d)\"", "chown -R $(whoami) build", "truncate -s $(stat -c %s ref) build.log",
		"mv -t \"$(mktemp -d)\" a.txt b.txt", "chmod -R $(stat -c %a ref) build", "rsync -a --delete src/ \"$(cat dest)\"/x",
		"find . -name '*.orig' -exec mv -t /tmp {} +", "find . -type f -exec chmod 644 {} +", "find build -exec chmod -R u+w {} +", "find . -type d -exec chmod 755 {} \\;",
		"d=build; rm -rf $ROOT/$d", "d=x; rm -rf \"$DIR\"/$d", "rm -rf $DESTDIR/usr/local/lib/foo", "rm -rf \"$OUT/dist\"", "tmp=$TMPDIR/x; rm -rf $tmp",
		"d=build; rm -rf $d/out", "git -C \"$dir\" clean -fdx", "git clean -fdx --dry", "git clean -n -dx", "git clean --dry-run -x", "git clean -d",
		"d=src/app; rm -rf \"${d/src/build}\"", "dir=build/; rm -rf \"${dir:0:-1}\"", "for f in *.txt; do rm -f \"${f/.txt/.o}\"; done", "rm -rf \"${dir/src/build}\"", "d=build; rm -rf ${d@Q}", "rm -rf ${X@U}", "for f in *.txt; do rm -f \"${f,,}\"; done",
		"files=(a.txt b.txt); mv \"${files[@]}\" /tmp/", "a=(a.txt b.txt); mv \"${a[@]}\" build", "srcs=(a.txt b.txt); mv -t build \"${srcs[@]}\"", "mv .git .git",
		"a=(755 build); chmod -R \"${a[@]}\"", "a=(-la src); ls \"${a[@]}\"/*.go", "a=(src/ build/); rsync -a --delete \"${a[@]}\"",
		"a=(755 build); chmod -R \"${a[@]=}\"", "a=(a.txt b.txt); mv \"${a[@]:-x}\" build", "a=(x); chmod -R 755 \"${a[@]:+build}\"", "a=(BUILD); chmod -R 755 \"${a[@],,}\"",
		"for f in *.txt; do mv \"$f\" \"${f%.txt}.bak\"; done", "a=(-a src/ build/x); rsync --delete \"${a[@]%x}\"",
		"alias ll='ls -l'\nll src", "hash -r; ls src", "hash -p /usr/local/bin/mytool mytool; mytool build", "a=(make -j4); \"${a[@]}\"", "args=(-la src); ls \"${args[@]}\"", "a=(mv a.txt b.txt); \"${a[@]}\"",
		"find . -type f | xargs chmod 644", "ls *.log | xargs gzip", "find . -name '*.sh' | xargs chmod +x", "git ls-files | xargs wc -l",
		"echo $[1+1]; mv build dist", "select x in a b; do mv $x build/; done", "coproc mv a.txt b.txt", "chmod --reference=a.txt b.txt",
		"coproc kill; mv a.txt b.txt 2>&1", "coproc kill; { mv a.txt b.txt; }", "coproc kill; x=$(mv a.txt b.txt)", "coproc kill; while true; do mv a.txt b.txt; break; done",
		"mv --backup=numbered a.txt b.txt", "mv --suffix=.bak -b a.txt b.txt", "mv a.txt b.txt {fd}>log", "declare -n ref=out; ref=build; echo $out",
		"for t in go node python3; do $t --version; done", "rm -f$v build/out.o", "git -C \"$dir\" $sub", "f() { rm -rf \"$1\"; }; f build", "f() { echo \"$@\"; }; f . / ~", "f() { git \"$@\"; }; f status; f diff", "f() { mv $1 $2; }; f a.txt b.txt", "f() { git $1 $2 $3; }; f commit -m x", "f() { chmod -R $1 $2; }; f 755 build", "f() { git \"$@\"; }; f status; f log -x", "sh -c 'echo \"$@\"' sh rm -rf /", "sh -c 'echo \"$1\"' sh .",
		"set -euo pipefail; rm -rf \"$1\"", "clean() { rm -rf \"$1\"; }; clean build; clean dist", "f() { mv \"$@\"; }; f a.txt b.txt", "chmod -R$v u+w build", "rm -rf $X-backup", "rm -rf \"$DIR\"-old", "rm -rf ${PREFIX}/lib/app", "rm -rf \"$HOME/.cache/app-$V\"",
	}
	for _, cmd := range ordinary {
		if d := bash(e, cmd); d.Behavior != Allow {
			t.Errorf("%q with permissions skipped: got %s critical=%v (%s), want allow", cmd, d.Behavior, d.Critical, d.Reason)
		}
	}
	// A rule that allows the command does not approve the destruction,
	// and dontAsk refuses it.
	for mode, want := range map[Mode]Behavior{ModeDefault: Ask, ModeAcceptEdits: Ask, ModePlan: Ask, ModeDontAsk: Deny, ModeBypass: Ask} {
		o := testOptions(mode, rules{allow: []string{"Bash(mv *)", "Bash(chmod *)", "Bash(git clean *)", "Bash(rsync *)", "Bash"}})
		e := mustEngine(t, o)
		for _, cmd := range []string{"mv .git /tmp/g", "chmod -R 000 .", "git clean -fdx", "rsync -a --delete src/ .", "shred -u *"} {
			if d := bash(e, cmd); d.Behavior != want || !d.Critical {
				t.Errorf("%q in %s: got %s critical=%v, want %s and critical", cmd, mode, d.Behavior, d.Critical, want)
			}
		}
	}
}

// TestTakesAll: with a way to see what is in a directory, targets that
// between them are everything in one that has to be kept are a sweep,
// however they are written.
func TestTakesAll(t *testing.T) {
	// (The made-up file system matches only the names it is given: the
	// directories are listed beside what is in them.)
	fs := fakeFS{files: []string{"/proj/a.txt", "/proj/b.txt", "/proj/Makefile", "/proj/src", "/proj/src/c.go", "/proj/build", "/proj/build/out.o",
		"/proj/.env", "/proj/.git", "/proj/.git/HEAD", "/home/u/notes.txt", "/home/u/Documents", "/home/u/Documents/d", "/home/u/.ssh"}}
	o := testOptions(ModeBypass, rules{})
	o.Resolve, o.Glob = fs.resolve, fs.goGlob
	e := mustEngine(t, o)
	for _, cmd := range []string{
		"rm -rf [!.]*", "rm -rf *?", "rm -rf [a-zA-Z]*", "rm -rf [a-m]* [n-z]* [A-Z]*", "rm -rf a.txt b.txt Makefile src build",
		"rm -rf ./[!.]*", "rm -rf /proj/[^.]*", "rm -rf ~/[!.]*", "rm -rf {a,b}.txt Makefile src build", "rm -rf ?* x",
		"mv [!.]* /tmp/", "mv -t /tmp a.txt b.txt Makefile src build", "chmod -R 000 [!.]*", "shred -u [!.]*", "truncate -s 0 [a-zA-Z]*",
		"rm a.txt b.txt Makefile src build", "rm -rf *.txt Makefile src build",
	} {
		if d := bash(e, cmd); d.Behavior != Ask || !d.Critical {
			t.Errorf("%q: got %s critical=%v (%s), want a critical ask", cmd, d.Behavior, d.Critical, d.Reason)
		}
	}
	for _, cmd := range []string{
		"rm -rf *.txt", "rm -rf [ab]*", "rm -rf a.txt b.txt", "rm -rf src build", "rm -rf src/*", "rm -rf build/[!.]*", "rm -f *.txt Makefile",
		"mv *.txt src/", "chmod -R u+w src build", "shred -u a.txt", "rm -rf a.txt b.txt Makefile src", "rm -rf ~/Documents/*",
	} {
		if d := bash(e, cmd); d.Behavior != Allow {
			t.Errorf("%q with permissions skipped: got %s critical=%v (%s), want allow", cmd, d.Behavior, d.Critical, d.Reason)
		}
	}
}

// TestWhatTheRemovalCheckLetsGo: what the check for dangerous removals and
// the other destroyers does not ask about, on purpose (doc.go says so). It
// is a seat belt for the usual accidents. A target that is only a variable
// gnosh knows nothing about runs, so that a session nobody watches can
// clean up after itself; and a flag, a subcommand or a word that only
// running the line produces is not something the check sees. This test is
// the record of where that line runs.
func TestWhatTheRemovalCheckLetsGo(t *testing.T) {
	o := testOptions(ModeBypass, rules{})
	o.Protected = []string{"/home/u/.config/gnosh", "/home/u/.local/share/gnosh"}
	e := mustEngine(t, o)
	for _, cmd := range []string{
		// A target that is only a variable nobody knows (a word a function
		// is handed counts as known only where the line writes it out).
		"rm -rf \"$tmp\"", "mv \"$f\" \"$f.bak\"", "chmod -R 755 \"$dir\"", "git clean -fdx \"$dir\"", "rm -rf ${PATH:0:1}", "git $cmd -fdx", "git \"$@\"",
		"read d; rm -rf \"$d\"", "f() { rm -rf \"$1\"; }; f \"$dir\"", "set -- \"$(cat list)\"; mv \"$@\"",
		// A flag or a subcommand that only running the line produces.
		"echo -rf . | xargs rm", "echo clean -fdx | xargs git", "echo clean -fdx | xargs timeout 5 git", "echo -R 000 . | xargs chmod",
		"chmod $(echo -R) 000 .", "git $(echo clean -fdx)", "echo -fdx | xargs git clean",
		"git clean -f$(echo dx)", "git clean $(echo -fdx)", "chmod -R$(echo) 000 .", "git $(x) status",
		// A pattern left a whole path when a variable is empty, that takes
		// in nothing that has to be kept.
		"rm -rf $OUT/build*", "rm -rf \"$TMPDIR\"/gnosh-*", "rm -f $X/tmp/*.log",
		// A variable before a name written out right under it is how a
		// build directory is named: /build where it is empty.
		"rm -rf $OUT/build", "mv $OUT/build /tmp/old",
		// A command's output without quotes, standing where the command
		// takes something other than a target, that prints more words than
		// the one it is there to be.
		"chown -R $(echo u .) build", "mv a.txt $(echo . /tmp/x)", "truncate -s $(echo 0 a.txt) b.txt",
	} {
		if d := bash(e, cmd); d.Behavior != Allow {
			t.Errorf("%q with permissions skipped: got %s critical=%v (%s); the record says it is let go", cmd, d.Behavior, d.Critical, d.Reason)
		}
	}
}

// rawCommands cuts at what ends a command outside quotes and nesting, and
// not inside a redirection.
func TestRawCommands(t *testing.T) {
	got := rawCommands("a <(b; c) | d 2>&1 &>x >|y; e \"f;g\" && h ${x;y} `i;j`\nk")
	want := []string{"a <(b; c)", "d 2>&1 &>x >|y", "e \"f;g\"", "h ${x;y} `i;j`", "k"}
	if !slices.Equal(got, want) {
		t.Errorf("rawCommands: got %q, want %q", got, want)
	}
	if got, want := nameTails("do x=$(mv <(date) 0) 'git' clean"), []string{"mv <(date) 0", "'git' clean"}; !slices.Equal(got, want) {
		t.Errorf("nameTails: got %q, want %q", got, want)
	}
}
