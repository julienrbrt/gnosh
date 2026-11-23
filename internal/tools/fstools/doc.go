// Package fstools is gnosh's file tools: the five things a model calls to
// look at and change files. Read shows a file, Write makes or replaces
// one, Edit changes part of one, Glob finds files by name, and Grep
// searches inside them.
//
// The names and arguments are the ones Claude Code documents for its tools
// of the same names, so that a person or a model who knows those finds
// these where they expect. The behaviour, the descriptions the model
// reads and every message are this package's own. It uses the standard
// library and nothing else: no search program is run, and the patterns
// and ignore rules are matched by its own code in the wild and ignore
// packages below it.
//
// # Using it
//
// New returns the five tools, each a core.Tool and a core.Describer:
//
//	tools := fstools.New(fstools.Options{})
//
// Options holds every name and every limit; a zero field takes its
// default. The tools keep nothing between calls. What a session has read
// and written lives in the core.ToolEnv.Store they are handed, so one set
// of tools serves any number of sessions and each session sees only its
// own state.
//
// # What every tool does the same way
//
// Arguments. A call's input is one JSON object. An argument the tool does
// not take is refused by name, since dropping it silently would change
// what the call means. A number may come as a string ("20") and a flag as
// the word "true"; anything else of the wrong kind is refused.
//
// Paths. A path should be absolute; a relative one is taken from the
// session's working directory. It is cleaned. A path that starts with "~"
// is refused with a message saying that "~" is not expanded, unless
// Options.HomeDir is set, in which case "~" and "~/..." stand for it.
// Symbolic links are followed, a loop of links is refused, and the path
// the links lead to is reported beside the path as written.
//
// Failures. Anything the model can do something about (a file that is not
// there, text that does not match, an argument that makes no sense) comes
// back as a result with IsError set and a message that says what
// happened, to what, and what to do next. Run returns a Go error only
// when the caller's context is cancelled.
//
// Describe. For Read, Glob and Grep, Describe returns ActionRead with the
// path the call would read or search. It checks the arguments and nothing
// on disk: whether the path exists is for the call to find out once it is
// allowed to look. For Write and Edit, Describe returns ActionEdit with a
// one-line summary and a Preview holding both sides of the change, worked
// out without writing anything; Run returns the same Display. When a
// Write or Edit would fail because of the state of the file (not read,
// changed on disk, no match), Describe returns that failure as its error
// together with an Action that still carries Kind and Paths, so that a
// permission layer can refuse the path before any message about the
// file's content reaches the model.
//
// Paths in an Action are the path as written and, when links lead
// elsewhere, the path they lead to: two entries. A rule that allows
// should hold for both; a rule that denies, for either.
//
// Meta. Each result's Meta holds the facts of the call under the key
// names Claude Code documents for its structured tool results
// ("filePath", "numLines", "type", ...), without the bulky ones: the text
// shown is in the result's content and both sides of a change are in its
// Display. Where a path was reached through links, "realPath" is where it
// led.
//
// # What a session has seen
//
// The package remembers, per session and per file, the file's size,
// modification time and (when it had the whole file in hand) hash, which
// of its lines the session has been shown, and whether the session wrote
// it. Files are known by where their links lead, so reading a file by
// one name and changing it by another is fine.
//
//   - Read adds the lines it shows. Reading a file page by page adds up:
//     once every line has been shown, the file counts as seen in full. A
//     line cut for length counts as shown. A read that showed nothing
//     (an offset past the end) adds nothing.
//   - A file is seen in full, too, when Read could only describe it: an
//     empty file, an image, a file refused as binary.
//   - Write and Edit record the file as they leave it. After Write the
//     whole file counts as seen. After Edit, what had been seen still is,
//     at the line numbers those lines now have, along with the new text.
//   - When the file on disk no longer matches what was recorded, the
//     record is void. With a hash and the file's bytes in hand the
//     comparison is exact, so a file that was only touched has not
//     changed; otherwise it goes by size and modification time.
//
// States lists a session's files for a caller that wants to show or
// record them. MarkRead tells the package that the model has seen a whole
// file some other way, such as a shell command that printed it. Forget
// does the opposite: the session no longer knows the files it names, and
// must read each again before it may change it. It is for a caller that
// has taken the part of the conversation in which they were read out of
// what the model is sent.
//
// Calls that change the same file in one session take turns, so that two
// edits made at once are both kept. Calls on different files do not wait
// for each other.
//
// # Read
//
// Arguments: file_path; offset, the line to start at, counting from 1;
// limit, the number of lines.
//
// Read shows the file's lines, each as its number, a tab, and the line,
// with no padding. Without a limit it shows Options.ReadDefaultLines
// lines (2000); with one, as many as asked. Either way one call shows at
// most Options.ReadMaxBytes of text (100000 bytes), and a line longer
// than Options.ReadMaxLineChars characters (2000) is cut and marked. The
// file is read as a stream: neither a huge file nor a huge line is ever
// held whole.
//
// After the lines come notes in square brackets, only when there is
// something to say: that the file goes on and which offset continues,
// with the file's line count when Read could count it (it reads on past
// the page for up to Options.ReadCountBytes to do so); that the lines end
// with CRLF; that the file starts with a byte order mark; that some bytes
// are not valid UTF-8.
//
// Line endings are shown as plain line breaks: a carriage return just
// before a line break is left out, and the note says the file has them.
// Bytes that are not valid UTF-8, and NUL bytes, are shown as U+FFFD. A
// byte order mark is left out.
//
// An empty file and an offset past the end give a notice, not an error. A
// directory, a missing file (with a hint when a like-named file is
// there), a device, socket or pipe, and a file that looks binary (a NUL
// byte in its first 8 KiB) are errors that say what the thing is. PNG,
// JPEG, GIF and WebP files, told by their first bytes, come back as an
// image block after a line of text, up to Options.ReadMaxImageBytes; a
// PNG, JPEG or GIF longer than 1568 pixels on a side or larger than that
// is shrunk to fit (shrink.go), and a WebP larger than that is refused.
// A PDF comes back as its text by pages (pdf.go), a notebook as its cells
// (notebook.go). allow_large, as Claude Code's Read takes it, reads past
// the usual limits as far as Options.ReadRoom says the context has room
// for. Every text Read stops short of Options.ReadResultMost, line
// numbers and all: the loop would cut a longer result, and the lines Read
// records as read must be the lines the model is shown.
//
// # Write
//
// Arguments: file_path; content, the whole of the new file.
//
// Write makes the file hold exactly content. Missing directories on the
// way are made. An existing file that is not empty must have been seen in
// full by the session and must not have changed on disk since; otherwise
// Write refuses, writes nothing, and says what to read. A new file and an
// empty file need no reading.
//
// The write is atomic: the content goes to a temporary file in the same
// directory, is flushed, and is renamed over the old file. The file keeps
// its permission bits and, where the system allows, its owner and group;
// a new file gets what the system gives any new file. Nothing temporary
// is left behind when a step fails, and directories made for a write that
// then failed are taken away again. Where the directory does not allow a
// temporary file or the rename, an existing file is written in place
// instead and the result says so.
//
// A file that the process may not write (by its permission bits, or on a
// read-only file system) is refused even where its directory would allow
// replacing it. Through a symbolic link, Write writes the file the link
// leads to and leaves the link alone; Options.RefuseLinkWrites makes it
// refuse instead and name the target. Hard links to the old file keep
// the old content, as with any atomic replace.
//
// Content that equals what the file already holds is not written again.
// A file that is not valid UTF-8 may be replaced, except by content that
// holds U+FFFD: that would be the garbling Read showed, written back for
// good.
//
// # Edit
//
// Arguments: file_path; old_string, the text to replace; new_string, the
// text to put there; replace_all.
//
// Edit replaces old_string with new_string. The match is exact, byte for
// byte, with one allowance: a line break in old_string matches a line
// break in the file whether the file writes it LF or CRLF. Nothing else
// is matched loosely and old_string is not a pattern. It must be found
// exactly once, unless replace_all is set, in which case every place it
// is found (left to right, none overlapping) is replaced. An empty
// old_string, and one equal to new_string, are refused.
//
// The session must have been shown every line the edit touches. A file
// read only in part can be edited in the part that was read. A file that
// has changed on disk since can still be edited where old_string matches
// it as it is now, at one place: the reply says the rest holds changes
// the session has not seen, and only the lines the edit put in count as
// seen afterwards. Not found, or found more than once, it is to be read
// again.
//
// Edit keeps everything it does not replace exactly as it was: the bytes
// before, between and after the replacements, the byte order mark, a
// missing line break at the end, the permissions. Line breaks in
// new_string are written the way the line they land in ends, so a CRLF
// file stays CRLF and a file with both keeps each where it was. An edit
// that would change nothing but line endings is refused.
//
// When old_string is not found, the error says why, as far as it can
// tell: the line numbers Read shows were copied along; a line break was
// asked for after the file's last line, which has none; line breaks were
// spelt out as backslash-n; or the text is there but for indentation,
// spaces at line ends, spacing, letter case or typographic quotes, in
// which case it names the lines and shows them. Failing that, it finds
// old_string's first line and says which later line differs. None of
// this changes what Edit accepts.
//
// Edit refuses a file that looks binary, a file that is not valid UTF-8
// (it cannot tell where the characters of an older encoding begin and
// end), and a file larger than Options.EditMaxBytes. On success it tells
// the model the line of each replacement and shows the lines around the
// first one as they now are.
//
// # Glob
//
// Arguments: pattern; path, the directory to search (the working
// directory when left out).
//
// Glob lists the regular files (and links to them) whose path below the
// search directory matches the pattern, as full paths, most recently
// changed first and by path among files changed at the same moment. It
// lists at most Options.GlobMaxResults (100) and says how many more
// matched. Finding nothing is an answer, not an error.
//
// The pattern syntax is the wild package's: "*", "?", "[...]", "{a,b}"
// and "**" for any number of directories. Letter case matters. An
// absolute pattern is searched from the directories it names, and path is
// then not used. Leading directories named outright in a pattern are gone
// to directly rather than searched for.
//
// Glob never lists anything inside a .git directory, never enters a
// directory through a symbolic link, and by default honours ignore rules
// and lists names that start with a dot; Options.GlobNoIgnore and
// Options.GlobSkipHidden change the last two. (Claude Code's Glob does
// not honour ignore rules by default; this one does because the brief for
// this package asks for it.) A directory named outright, in path or at
// the start of the pattern, is searched whatever the rules say about it,
// and so is a name the pattern spells out in full anywhere in it: .env,
// **/.env and **/dist/app.js list those files though .gitignore names
// them.
//
// # Grep
//
// Arguments: pattern; path, a file or directory; glob and type, which
// narrow the files; output_mode; -i; -n; -o; -A, -B, -C and context;
// multiline; head_limit and offset.
//
// The pattern is a regular expression in Go's regexp syntax (RE2), which
// has no look-around and no backreferences; an error for a pattern that
// uses them says so. A match lies within one line, and a carriage return
// before the line break is not part of the line. With multiline the file
// is searched whole: the pattern may run over line breaks, "^" and "$"
// still match at every line, "." still stops at a line break unless the
// pattern sets (?s), and CRLF is read as a line break.
//
// In a directory Grep searches every file below it except what ignore
// rules leave out, names that start with a dot, anything in .git, files
// that look binary and anything reached through a symbolic link; three
// options turn the first, second and fourth of those off. A file or
// directory given as path is searched whatever it is called. glob keeps
// the files whose name matches (or, for a glob with a slash, whose path
// below the search directory does), and reaches them even where they are
// hidden or ignored, as ripgrep's glob does; a leading "!" turns it round,
// and then leaves out directories it names as well ("!vendor"; "!vendor/"
// directories only). type keeps one kind of file from the table in
// filetypes.go.
//
// The three modes: files_with_matches lists the files that have a match;
// count lists each such file with its number of matching lines (of
// matches, with multiline) and then the total over all files, which
// paging never reduces; content shows the matching lines under the path
// of their file, as "number:line". With -A, -B or -C the lines around
// each match are shown as "number-line", and a line of "--" stands
// between stretches of one file that are not next to each other. -o
// shows each matched text on a line of its own and no context. A line
// longer than Options.GrepMaxColumns bytes is cut around its first match.
//
// Order is fixed and documented: files come most recently changed first
// and by path among files changed at the same moment; lines come in file
// order. head_limit and offset page through that order by entries: files
// in the two file modes, lines (matching and context alike, not
// separators or headings) in content mode. Without a head_limit,
// Options.GrepHeadLimit (250) applies; 0 means no limit. Whatever the
// limit, one call returns at most Options.GrepMaxOutputBytes. A note at
// the end says what part was shown and which offset continues; an offset
// past the end says how many entries there are.
//
// Grep searches with several goroutines and always searches everything
// before it answers, so the counts and the order do not depend on timing.
// It counts first and then reads again only the files whose lines fall on
// the page, so a search with a great many matches does not hold them all.
// Directories and files it could not read, and files too large for a
// multiline search, are named in a note rather than passed over in
// silence. When nothing matches, the result says how many files were
// searched, so that "no match" can be told from "nothing was looked at".
//
// # Limits on searches
//
// A Glob or Grep call that takes longer than Options.SearchTimeout (20
// seconds) fails with a message that says so. It never returns part of an
// answer, since a partial list would read as a complete one.
//
// # Ignore rules
//
// The ignore package follows git's documented pattern format, and its
// tests compare it with git itself over thousands of generated cases. It
// reads .gitignore in every directory (other names can be added) and the
// repository's .git/info/exclude. It does not consult git's index, so a
// tracked file that matches a pattern is left out all the same; it does
// not read the user-wide excludes file; and letter case always matters.
package fstools
