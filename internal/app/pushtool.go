package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gnosh-ai/gnosh/internal/core"
)

// This file is PushNotification, Claude Code's tool by which the model
// calls the person back to the session: a long task done while they were
// away, a decision only they can make. gnosh sends it the ways it already
// tells a person who is away that the session waits: a desktop note
// through the terminal, by their channel (preferredNotifChannel: OSC 9 or
// 99, or the bell), and their Notification hooks, with the type
// push_notification, which a hook can forward to a phone. When the person
// is at the terminal (a key pressed in the last half minute) nothing is
// sent, as in Claude Code: what the model writes reaches them already.
// One is sent in a minute at most, so that a model in a loop cannot keep
// calling the person; the text is one line, without control characters,
// which a terminal would act on.

const pushName = "PushNotification"

// pushWait is how long the tool waits for the front end's word, and
// pushEvery the least time between two notifications sent.
const (
	pushWait  = 2 * time.Second
	pushEvery = time.Minute
)

// pushTool is PushNotification.
type pushTool struct{ l *live }

func (t *pushTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name: pushName,
		Description: "Calls the user back to this session with a notification, when they may have walked away and something here is worth coming back for: a long task finished, a build is ready, a decision only they can make. " +
			"It pulls them from whatever they are doing, so do not use it for routine progress or a quick answer they are watching for; use it when they asked to be told. " +
			"One line under 200 characters, no markdown, leading with what they would act on (\"build failed: 2 auth tests\"). When they are at the terminal nothing is sent, and the result says so.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string","minLength":1,"description":"The notification: one line, under 200 characters."},"status":{"const":"proactive"}},"required":["message"],"additionalProperties":false}`),
	}
}

func (t *pushTool) Describe(call core.ToolCall, env *core.ToolEnv) (core.Action, error) {
	return core.Action{Kind: core.ActionOther, Summary: "Notify the user"}, nil
}

func (t *pushTool) Run(ctx context.Context, call core.ToolCall, env *core.ToolEnv) (core.ToolResult, error) {
	var in struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(call.Input, &in); err != nil {
		return core.ErrorResult("The input could not be read: " + err.Error()), nil
	}
	msg := pushText(in.Message)
	if msg == "" {
		return core.ErrorResult("The message is empty: nothing was sent."), nil
	}
	l := t.l
	// (Meta "notSent" says why, for the screen, in the person's words: the
	// result's are the model's, "the user ... reads what you write".)
	notSent := func(text, why string) core.ToolResult {
		res := core.TextResult(text)
		res.Meta = map[string]any{"notSent": why}
		return res
	}
	if last := l.pushLast.Load(); last != nil && l.p.now().Sub(*last) < pushEvery {
		return notSent("Notification not sent: one was sent less than a minute ago.", "one went out under a minute ago"), nil
	}
	reply := make(chan string, 1)
	l.deliver(&PushEvent{Message: msg, Reply: reply})
	select {
	case said := <-reply:
		if strings.HasPrefix(said, "sent") {
			now := l.p.now()
			l.pushLast.Store(&now)
			return core.TextResult("Notification " + said + "."), nil
		}
		why := "nothing here reaches you with one"
		if strings.Contains(said, "at the terminal") {
			why = "you were at the terminal"
		}
		return notSent("Notification "+said+".", why), nil
	case <-time.After(pushWait):
		return notSent("Notification not sent: this session's interface does not send notifications.", "this screen sends none"), nil
	case <-ctx.Done():
		return core.ErrorResult("The call was stopped before the notification was sent."), nil
	}
}

// pushText is a notification's text: one line, its blanks folded, cut to
// 200 characters. Control characters (an escape, a bell, which a terminal
// acts on) count as blanks, and the marks that turn the direction of text
// around, which can make a line read as another, are left out.
func pushText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Bidi_Control, r):
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= 200 {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:199])) + "…"
}
