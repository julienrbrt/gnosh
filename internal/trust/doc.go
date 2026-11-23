// Package trust is the gate between a folder's own settings and the person
// who starts gnosh in it.
//
// Starting an agent in a repository you have just cloned must not run the
// stranger's code. A project's settings can add a hook (a command that
// runs), start a server, let tools run without asking, point a provider at
// someone else's machine, set environment variables and name machines to
// log in to. So gnosh asks first, in plain words that say what could go
// wrong, and until the answer is yes a project can only take things away
// from the agent.
//
// The package holds the gate's data and rules. It runs nothing from the
// folder and never asks git: it only reads files.
//
// # What "the folder" is
//
// Resolve works it out for the directory gnosh was started in:
//
//   - Root is where the project begins: the top of the git repository that
//     holds the directory, or the directory itself.
//   - Key is the folder the record is kept under. For a repository it is
//     the main checkout, so that its worktrees share one answer. A worktree
//     shares it only when the repository lists that worktree as its own: a
//     folder cannot borrow a repository's trust by naming it in a .git of
//     its own, and a .git that is a link is never followed.
//   - A repository whose own git settings place its work tree somewhere
//     else is keyed on the folder gnosh was started in.
//   - The home directory, and any folder that holds it, is trusted for one
//     session at a time and never recorded.
//
// # The record
//
// Store keeps the trusted folders in the user's state directory, never in
// a project: a directory with one small JSON file per folder, named after
// a digest of the folder's path, written whole and renamed into place. An
// entry stops counting when
//
//   - its file is not the user's own, can be written by others, or is not
//     as gnosh wrote it (each file carries a digest of its fields);
//   - the folder at the recorded path is not the folder that was trusted:
//     the entry remembers the folder's device and inode numbers, so a
//     different folder put at the same path does not inherit the answer,
//     and a folder moved elsewhere is asked about again;
//   - it names the home directory or a folder that holds it.
//
// Outside a repository, a trusted folder covers the folders under it that
// are not repositories of their own.
//
// # What an untrusted folder may still do
//
// Only restrict. Restricts is the rule: a deny rule, a rule that makes a
// tool ask first, a tool turned off and a skill hidden from the model
// apply; everything else waits. The config loader is
// given Restricts as its filter, reads the project's files with the same
// reader either way, and returns the settings that wait as a list. That
// list is the input of the question, so the question can never show less
// than what would run.
//
// Text for the model that runs nothing is not this package's to hold back:
// context files, the skills inside the folder, custom commands and
// sessions are used in an untrusted folder too.
//
// # The question, and the ways to trust
//
// NewQuestion turns the list into the question: the folder, what its
// settings would do, and under that what could go wrong, one plain
// sentence per kind of harm and only for the kinds present. Everything
// taken from the folder's files is escaped. Decide says when to ask:
// only with a person there, and only when something is held. Without a
// person (gnosh -p) nothing is asked: the settings stay out and
// Notice is the one line for stderr. The command line has three ways in:
// gnosh trust (Store.Trust, then Report), gnosh trust --list and --forget
// (Store.List, Store.Forget), and --trust-folder, which trusts for one run
// and records nothing.
//
// GitPrograms lists what a repository's own git settings name that git
// would run on a mere look. It belongs in the question, and while it is
// not empty in an untrusted folder the agent is given no git status and
// its git commands ask first. LeadsOut says whether a path is a link
// that leads out of the folders given, which is why a project's skill,
// command or agent file linked to a place outside the project is not
// read.
package trust
