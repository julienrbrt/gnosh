// Package shelltool is the model's way to run shell commands, on this
// machine and on the machines the person declared: the Bash tool,
// background commands, their output, and stopping them.
//
// A Manager belongs to one session. It hands out the tools (Tools), keeps
// track of every process it starts, and stops them all when it is closed
// (Close). The tools are Bash, which runs a command; TaskOutput, which
// returns what a background command has printed since it was last asked;
// TaskStop, which stops a background command; and Monitor, which runs a
// command in the background and passes on what it prints as it comes
// (monitor.go), where Options.OnMonitor gives the lines somewhere to go.
// Their names and the
// names of their arguments are Claude Code's, but for host, which is
// gnosh's own, and all of them are in names.go.
//
// The package does not decide which commands may run. A call says what it
// would do through core.Describer, with the command exactly as the model
// wrote it, and the permission rules that judge it live elsewhere. The few
// rules it does have are about how a thing is done, not whether it may be
// (see "The tool's own rules").
//
// # Where a command runs
//
// Where a command runs is a fact of the call, never something written into
// the command. Bash takes host: the name of a machine the person declared
// (Options.Hosts). Left out, or given as LocalName, the command runs on
// this machine; a name that was not declared is an error that lists the
// names there are, or, with none declared, tells the model to ask the user
// to declare one and not to reach a machine some other way.
//
// Every result of a command, a local one too, begins with one line that
// says where it ran (stamp), so that the model reads the machine beside
// the output and a path is never taken for a path on another machine:
//
//	<host>local (dev@box)</host><cwd>/home/dev/project</cwd>
//	<host>render (root@203.0.113.10)</host><cwd>/root</cwd>
//
// The directory is the one the command started in, on that machine. A
// result with no output has the line too. The same facts are in the
// result's Meta ("host", "machine", "cwd"), for hooks and the transcript;
// the Display, which is for the person, has the machine in its title and
// not the line. A call that was refused before anything ran has no such
// line: it ran nowhere.
//
// # Commands on other machines
//
// A command for a host is run by the system's ssh (Options.SSH), started
// in a session of its own exactly as a local shell is, with the null
// device as its standard input. sshArgs lists what it is given and why:
// no terminal on the other side, never a question (a password, a
// passphrase, an unknown key: what would ask fails instead), a limit on
// connecting (Options.ConnectTimeout), "--" before the destination so that
// nothing after it can be read as an option, and the command as one
// argument, which no shell on this machine ever reads. Keys, agents,
// ports, jump hosts and shared connections are the business of the
// person's own ssh configuration.
//
// A machine's key must already be known to this account's ssh. gnosh
// passes StrictHostKeyChecking=yes, so a key that is not on record, or
// that has changed, means no connection: the result says so in plain
// words, says that the user has to connect once by hand and check the
// key, and tells the model not to reach the machine another way. gnosh
// never accepts a key by itself, and does not let ssh do it for it.
//
// On the other machine the account's login shell is handed one line: a
// short lead of gnosh's (remoteLead) and then the command, in single
// quotes, as the argument of eval. So the command is read by that shell
// exactly as it was written, several lines and here-documents included.
// The lead reports the directory the shell is in on standard error, sends
// standard error where standard output goes from then on, and sets the
// same few variables a local command gets (GNOSH=1 and the rest). It
// needs a login shell that reads sh: sh, bash, dash, ksh or zsh.
//
// That gives two streams on this side. ssh's standard output is the
// command's output, its two streams in the order written, and is treated
// like a local command's: kept in a file on this machine, shown live, cut
// to the same length. ssh's standard error is kept apart (sideChannel): it
// carries the report that the command started, and whatever ssh itself has
// to say. That is how a failure of the connection is told from a failure
// of the command, which the exit code alone cannot do, since ssh ends with
// 255 for its own failures and a command may end with 255 too:
//
//   - no report, exit code 255: ssh never got the command started. The
//     result says why in plain words (the machine refused the connection,
//     did not answer in time, is not known by that name, took none of the
//     account's keys, has an unknown or a changed key), says that nothing
//     ran, and carries ssh's own words. Meta has ReasonNoConnection and no
//     exit code.
//   - a report, exit code 255, and ssh said something afterwards: the
//     connection broke while the command ran. How the command ended is not
//     known, and the result says so. Meta has ReasonConnectionLost.
//   - a report, and any exit code: the command's own.
//   - no report and another exit code: the lead did not run as written
//     (a login shell that is not sh, an account tied to one command). The
//     result says that whether the command ran is not known.
//
// What does not carry over to another machine:
//
//   - The working directory. A command there starts in the account's home
//     directory, every time; the session's directory is a path on this
//     machine and is never used there. Nothing carries over between two
//     commands on a host, not even the directory, and a command there
//     never moves the session (the result's Cwd is always empty). A
//     command that is only a cd gets a note that says so.
//   - Background commands. run_in_background with host is refused, a
//     command there is stopped when its time is up even where local ones
//     move to the background (Options.TimeoutToBackground), and
//     Manager.Background leaves it where it is. A command on another
//     machine lives only as long as its call holds the connection, and its
//     output file would be a path on this machine holding another
//     machine's output, read later with nothing to say so.
//   - Stopping. A stop (the time limit, a cancelled call, the manager
//     closing, too much output) ends the ssh on this machine, with
//     everything it started here, exactly as it ends a local command, and
//     that closes the connection. Nothing on the other machine is
//     signalled: ssh without a terminal has no way to do it. A program
//     there that is printing is ended by its own system at its next write;
//     one that prints nothing (a sleep, a server, a job that writes to a
//     file) runs on, where gnosh can neither see it nor stop it. The
//     result of a stopped command says exactly that.
//
// TryHost logs in to a host and runs nothing but the lead, for whoever
// wants to know that a host can be used. Nothing calls it unasked.
//
// CopyFromHost copies one file back from a host (fetch.go), for whoever
// has to hand a file on another machine to the person, such as the
// Deliver tool. It takes the same road as a command and no other: the
// same ssh with the same arguments runs one command there (CopyCommand,
// which is what a caller shows the person and holds the permission rules
// against), and the file comes down ssh's standard output. There is no
// scp. The file is taken only whole and only up to the size the caller
// names; what went wrong otherwise is said in the words a command for
// the host would get.
//
// # How a command runs
//
// Each command gets a new shell: /bin/bash where there is one, /bin/sh
// otherwise, or Options.Shell. The shell is not interactive and reads no
// start-up files of its own accord. It runs in the session's working
// directory, with the null device as its standard input, with stdout and
// stderr going down one pipe so that the two stay in the order written,
// and as the leader of a new session. A new session means a new process
// group, which is what gets signalled when the command must stop, and it
// means no terminal: a program that would open the terminal to ask for a
// password fails at once instead of waiting where nobody will answer.
//
// The command is never pasted into a script. It reaches the shell as an
// argument of its own (through a file when it is longer than 64 KiB, which
// is more than an argument may be) and is handed to eval, so that quotes,
// dollars, backslashes, here-documents and several lines mean what they
// mean at a prompt, and a mistake in the command is reported by the shell
// in its own words. Two things differ from "sh -c": the shell's messages
// say "eval" where they would say "-c", and the shell holds two variables
// of the wrapper's, __gnosh_cwd and __gnosh_cmd.
//
// On Linux the shell first sets its oom_score_adj to 1000, the most, which
// what it starts inherits: when the machine runs out of memory the kernel
// kills a command of the model's before anything else (oom_linux.go).
//
// The environment is the parent's (Options.Environ) with these laid over
// it, and then Options.Env over those:
//
//	PAGER=cat               no pager waits for a key
//	GIT_PAGER=cat           nor git's
//	GIT_TERMINAL_PROMPT=0   git fails rather than ask for a password
//	TERM=dumb               programs keep to plain text
//	GNOSH=1                 a script can tell that gnosh is running it,
//	AGENT=gnosh             and that an AI agent is,
//	AI_AGENT=gnosh          under either of the names in use
//	PWD=<working directory> so the shell's idea of where it is, is right
//	OLDPWD                  removed: it would be the harness's, not the session's
//
// Last of all comes GNOSH_SHELL_ID (MarkVar), set to 32 hex digits made at
// random for the session, which nothing else may set: the processes a
// command starts inherit it, and by it gnosh knows them as its own when
// the session ends.
//
// # What carries over
//
// Only the working directory. When the shell exits, by reaching the end,
// by exit, or by an error, a trap writes the directory it is in to a file.
// If that directory is inside one of the session's roots, the result's Cwd
// names it and the session moves there; it is spelled under the root the
// way the session spells the root, whatever links lie between. If it is
// outside every root, Cwd stays empty and the result tells the model that
// the working directory was reset to where it was. A command that replaces
// the shell with exec, or sets an EXIT trap of its own, leaves no word of
// where it ended; the session stays put and the result says so. A command
// that was stopped never moves the session. Variables, functions, aliases
// and shell options are gone with the shell.
//
// # Time
//
// A foreground command may run for Options.DefaultTimeout (two minutes),
// or for the timeout the call asks for, up to Options.MaxTimeout (ten
// minutes); a larger number is lowered to that, as Claude Code does. When
// the time is up the command is stopped and the result carries the output
// so far. With Options.TimeoutToBackground it is moved to the background
// instead, where it runs on; Manager.Background does the same when the
// person asks. A background command has no time limit unless the call or
// the options give it one.
//
// # Stopping
//
// A command is stopped when its time runs out, when the context of the
// call waiting for it is cancelled, when TaskStop or Manager.Stop asks,
// when it has printed more than Options.MaxOutputBytes, and when the
// manager is closed. Every process group in the command's session gets
// SIGTERM (and SIGCONT, so that a stopped process can act on it); after
// Options.TermGrace, whatever is left gets SIGKILL; then the shell is
// reaped and the session is looked at until it is empty. The process group
// alone would miss the few programs that move to a group of their own, as
// timeout(1) does, so the session is searched: through /proc on Linux, and
// through the kernel's process table on macOS. Elsewhere only the shell's
// group is signalled. A process that starts a session of its own (setsid,
// a proper daemon) is not reached, and is not meant to be.
//
// A signal to a group reaches what is in the group at that instant. A
// process forked a moment later has missed it, and so, in effect, has one
// that was just then turning from a copy of its parent shell into the
// program it was forked to run. Either would leave the shell waiting for
// a child that knows nothing of the signal. So while the manager waits it
// keeps looking at the session, and sends the signal to any process that
// was not there when it went out, or has become another program since.
//
// Nothing waits without a limit. If a process is still alive
// Options.KillWait after SIGKILL, the result names it and says that it
// could not be stopped.
//
// # A process left running
//
// A command that ends but leaves something running that still holds its
// output open (sleep 300 &, a server started with &) does not hold the
// call up. Once the shell has exited, the kernel is asked whether any
// process still has the other end of the output pipe open. If none has,
// what is in the pipe is all there will be, and it is read to the end,
// however slow a busy machine is about it. If one has, the output is given
// Options.PipeGrace to end. Then the call returns with what had been
// printed by then and with the command's own exit code, and says that a
// process was left running, which process group it is in, and how to stop
// it.
//
// That process is not killed. What it prints from then on is still read,
// so that it is never blocked on a full pipe or killed by a closed one,
// and goes to the command's output file. It is stopped when the manager is
// closed, but only if it can be shown to be the command's: its group's id
// may by then belong to something else. A process seen as the command
// ended that is still there, the same one, shows the whole session is
// still the command's; otherwise only processes carrying the session's
// GNOSH_SHELL_ID in their environment are (on Linux: macOS shows no
// process another's environment). Listening costs a goroutine and
// two open files for each such command, so at most 32 are listened to at
// once; what a further command leaves behind is stopped at once, and that
// command's result says so.
//
// A process that let go of the command's output (cmd >log 2>&1 &, a
// server started so) does not hold the call up either: the command ends
// cleanly. It is still in the command's session, and the result says it
// was left running; it is stopped when the manager is closed, as one that
// holds the output is, if it can be shown to be the command's. So is a
// server a launcher started that has ended since (the launcher seen at the
// end is gone, the server carries the mark; on Linux), and one that held the output
// at the end and let go of it later (the task then says it runs on).
// Sessions on the books whose processes have all ended are forgotten as
// new ones come. One that started a session of its own (setsid) is left
// alone, at Close too: that is the way to keep a process past the
// session.
//
// # Output
//
// A command's output is written to a file of its own as it arrives, at
// most Options.MaxSpoolBytes of it (64 MiB), and only its first and last
// few tens of kilobytes are held in memory, so a command may print any
// amount. The model is shown at most Options.MaxOutputChars characters
// (30,000), or Options.MaxFailedOutputChars (10,000) of a command that
// failed. Longer output is shown as its start and its end, cut between
// lines where that costs little, around one line of the form
//
//	[1234 lines omitted, full output in /path/to/cmd-7.out]
//
// NUL bytes are dropped and bytes that are not valid UTF-8 become U+FFFD.
// The file keeps the bytes as they were. It is removed at once when the
// model was shown everything, and otherwise kept until Close, or until
// the files of finished foreground commands add up to more than
// Options.SpoolBudget, when the oldest go.
//
// A result is the line that says where the command ran, then the output,
// with a line above it when the command did not simply end ("[timed out
// after 120 s and was stopped; the output so far is below]") and bracketed
// notes under it: the exit code when it is not 0, a process left running,
// where the working directory is now. A non-zero exit code makes the
// result an error, except exit code 1 from the programs for which 1 is an
// answer and not a failure (grep and its kin, find, diff, test, git diff,
// git grep; see harmlessExit). The exit code of a command that ran is
// always in the result's Meta, with the other facts of the call; the keys
// are in names.go.
//
// While a foreground command runs, its output is passed to the session's
// Progress function a line at a time (a line without its newline after a
// tenth of a second). The command never waits for the listener: output the
// listener is too slow for is dropped for the listener, who is told how
// much.
//
// # Background commands
//
// With run_in_background, Bash returns as soon as the command has started,
// with an id ("bg1", "bg2", ...) and the path of the output file.
// TaskOutput returns the output the model has not been given yet, cut to
// the same length as a foreground result, and says whether the command is
// still running or how it ended; with a filter, a regular expression, it
// returns only the new lines that match. TaskStop stops the command as
// described above. At most Options.MaxBackground (50) run at once.
// Options.NoOutputTool leaves TaskOutput out, for a session whose model
// reads the output file with the file-reading tool instead.
//
// When a background command ends, Options.OnEnd is told: which command,
// how it ended, how long it ran, and the end of its output. Each end is
// announced once: not if TaskOutput or TaskStop has already shown it to
// the model, and not if Close caused it. Manager.Tasks lists the
// background commands and Manager.Stop stops one, for the person's own
// controls.
//
// A Monitor call's command is a background command like the others, with
// a watcher on its output: Options.OnMonitor is offered its lines every
// second, at most monitorLines of them (monitorBytes in all, each line cut
// at monitorLineBytes), and what is turned down is offered again with what
// came after. Its last lines are offered before the news of its end.
//
// # The tool's own rules
//
// rules.go holds the few things Bash refuses whatever the permissions say,
// and the notes it puts under a failure. Each is there to tell a model, at
// the moment it matters, what to do instead; none is a wall, and a full
// path or a script gets past every one. They read only the first line of a
// command that is not blank or a comment, so a file written with a
// here-document may say anything. The person's own command line
// (PersonsBash) is held to none of them.
//
//   - With hosts declared, a command whose first line runs ssh or scp, in
//     any of its commands and behind env, sudo, timeout and the like, is
//     refused with the declared hosts and the way to use one. With no host
//     declared nothing is refused on that account.
//   - An argument Bash does not take is an error that names it. Passed
//     over in silence, a made-up argument (a working directory, say) would
//     leave the model believing the command ran as it asked.
//   - With Options.LongSleep set, a foreground command of a session's own
//     that begins by sleeping that long or longer is refused, with what to
//     do instead: run the wait in the background and be told when it ends.
//     Several shorter sleeps one after another count together.
//   - A command on this machine whose first line runs, as a program that
//     is not installed, the name of one of the session's tools
//     (Options.ToolNames), or a name Options.Misread says was meant for a
//     tool (gnosh's: mem.Show() and its kin, typed into a shell), is
//     refused before it runs, with what to call instead. It would only
//     fail, after a person was asked about it.
//   - Under a failure: exit code 127 gets a note that the program is not
//     installed on that machine, to do it another way or ask the user, and
//     not to search the disk; a lookup that found nothing (which, command
//     -v) gets the same; a command whose first word is one of the
//     session's tools (Options.ToolNames) is told to call the tool; and
//     Options.Misread may say, in place of all these, what a failed
//     command was meant to be (gnosh's: Gno for mem, typed into a shell).
//
// # Closing
//
// Close stops everything that is running, waits until it is gone, and
// removes the manager's output files and the directory it made for them.
// It is bounded like everything else, and its error says what, if
// anything, could not be stopped.
package shelltool
