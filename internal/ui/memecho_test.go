package ui

import (
	"testing"

	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/text"
)

// A receipt does not quote again what the call's code says.
func TestWithoutEchoes(t *testing.T) {
	ask := `mem.Ask("decode-gothfry-lyrics", "Jarvis settled it: it holds no wallet, and the /agent studio-kid reservation only counts from your own browser (the key shows once to you).")`
	for _, c := range []struct{ code, receipt, want string }{
		{
			// Jae's screen, 2026-11-21: the question, then the question again.
			ask,
			"asked `decode-gothfry-lyrics/jarvis-settled-holds-no` \"Jarvis settled it: it holds no wallet, and the /agent studio-kid reservation onl…\" (for the user); `decode-gothfry-lyrics` waits on it; next: `decode-gothfry-lyrics/pull-comments-quoting-lyrics` \"pull comments quoting lyrics\"",
			"asked `decode-gothfry-lyrics/jarvis-settled-holds-no` (for the user); `decode-gothfry-lyrics` waits on it; next: `decode-gothfry-lyrics/pull-comments-quoting-lyrics` \"pull comments quoting lyrics\"",
		},
		{
			`t := mem.Todo("write the tests")` + "\nmem.Push(t)",
			"added `write-the-tests` \"write the tests\"\npushed `write-the-tests`",
			"added `write-the-tests`\npushed `write-the-tests`",
		},
		{
			// A title the code does not hold says something.
			`mem.Done("ship", "tagged v2")`,
			"done `ship` \"ship the release\" → tagged v2",
			"done `ship` \"ship the release\" → tagged v2",
		},
		{
			// Quotes inside the text, as mem quotes them.
			`mem.Ask("", "say \"hi\" to them")`,
			"asked `q-say-hi-to-them` \"say \\\"hi\\\" to them\" (for the user)",
			"asked `q-say-hi-to-them` (for the user)",
		},
		{
			// A raw string over two lines, which a receipt's line shows
			// with ⏎.
			"mem.Todo(`first line\nsecond line`)",
			"added `first-line` \"first line ⏎ second line\"",
			"added `first-line`",
		},
		{
			// Too short to be sure of.
			`mem.Todo("go")`,
			"added `go-2` \"go\"",
			"added `go-2` \"go\"",
		},
		{
			// A text cut short is the beginning of another text: not ours.
			`mem.Todo("write")`,
			"added `w` \"writes the docs…\"",
			"added `w` \"writes the docs…\"",
		},
		{"println(1)", "1", "1"},
		{`mem.Todo("unclosed`, "added `x` \"unclosed", "added `x` \"unclosed"},
	} {
		if got := withoutEchoes(c.receipt, c.code); got != c.want {
			t.Errorf("withoutEchoes(%q, %q)\n got %q\nwant %q", c.receipt, c.code, got, c.want)
		}
	}
}

// A mem call that asks the user something shows the question once: in its
// code. Its receipt says what mem did with it.
func TestGnoCallQuestionShownOnce(t *testing.T) {
	g := newRig(t, 100, 30, text.NoColor)
	g.keys(`"ask me" <Enter>`).start()
	code := `mem.Ask("decode", "Open the gno.world chat and run /agent studio-kid from your browser, then paste me the key.")`
	args := map[string]any{"name": "mem", "code": code}
	g.ev(toolCall("c1", "gnovm_run"), replyEnd(core.StopToolUse), toolStart("c1", "gnovm_run", args), toolEnd("c1", "gnovm_run", args,
		core.ToolResult{Content: []core.Block{core.Text("asked `decode/open-the-gno-world-chat` \"Open the gno.world chat and run /agent studio-kid from your browser, then pas…\" (for the user); `decode` waits on it\n")}}, 0))
	g.once("Open the gno.world chat and run /agent studio-kid")
	g.committedHas("asked `decode/open-the-gno-world-chat` (for the user); `decode` waits on it")
}
