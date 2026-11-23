package shelltool

// oomFirst opens every command's shell on Linux: the shell asks the kernel
// to kill it, and what it starts, first when the machine runs out of
// memory (oom_score_adj 1000, the most; a process may always raise its
// own). A runaway build of the model's is then what goes, not the model's
// server on the same machine or the person's other work, as happened on
// the AI boxes. Claude Code caps the memory of its commands with a cgroup
// when told to (CLAUDE_CODE_TOOL_MEMORY_LIMIT); a cgroup of one's own is
// seldom there to be had in a terminal over ssh, and this needs nothing.
const oomFirst = `{ echo 1000 >/proc/self/oom_score_adj; } 2>/dev/null; `
