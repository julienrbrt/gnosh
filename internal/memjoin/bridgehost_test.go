package memjoin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gnosh-ai/gnosh/internal/bridge"
	"github.com/gnosh-ai/gnosh/internal/core"
	"github.com/gnosh-ai/gnosh/internal/transcript"
)

// askSide sends one request and answers the side requests the bridge
// writes before its reply with answer, which is told each.
func (h *host) askSide(req map[string]any, answer func(bridge.SideRequest) string) bridge.Reply {
	h.t.Helper()
	line, err := json.Marshal(req)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.in.Write(append(line, '\n')); err != nil {
		h.t.Fatalf("writing %s: %v", line, err)
	}
	for {
		if !h.out.Scan() {
			h.t.Fatalf("no reply to %s: %v", line, h.out.Err())
		}
		var side struct {
			Side *bridge.SideRequest `json:"side"`
		}
		if json.Unmarshal(h.out.Bytes(), &side) == nil && side.Side != nil {
			if answer == nil {
				h.t.Fatalf("a side request nobody expected: %s", h.out.Bytes())
			}
			ans, _ := json.Marshal(map[string]any{"op": "side_answer", "side_id": side.Side.ID, "answer": answer(*side.Side), "stop": "end_turn"})
			if _, err := h.in.Write(append(ans, '\n')); err != nil {
				h.t.Fatal(err)
			}
			continue
		}
		var rep bridge.Reply
		if err := json.Unmarshal(h.out.Bytes(), &rep); err != nil {
			h.t.Fatalf("reply %s: %v", h.out.Bytes(), err)
		}
		return rep
	}
}

// callIn is runCall for session with the conversation through the
// calling reply, as a host sends a call to mem (bridge.md §11).
func callIn(c *conv, id, code, vm string) map[string]any {
	req := runCall(id, code, vm)
	req["session"] = c.session
	req["entries"] = c.entries
	return req
}

func endIn(c *conv, text string) map[string]any {
	reply := core.AssistantText(text)
	c.add(transcript.AssistantEntry(reply, core.StopEndTurn))
	return map[string]any{"op": "end", "session": c.session, "entries": c.entries, "reply": reply, "finished": true}
}

// A close whose result claims a file nothing made is refused by the claim
// check, as in a session, and mem keeps nothing; the turn that would end
// standing in the todo is sent back once (close-check.md §3.3, §5).
func TestBridgeCloseCheckAndSendBack(t *testing.T) {
	w := newWorld(t)
	h := w.bridge()
	h.ask(map[string]any{"op": "hello", "protocol": 1, "client": "test", "side": true})
	h.ask(map[string]any{"op": "prompt", "session": "S1", "first": true, "text": "write report.md"})

	c := &conv{session: "S1"}
	c.prompt("write report.md")
	c.call("c1", `mem.Todo("write the report").Push()`, "mem")
	push := h.askSide(callIn(c, "c1", `mem.Todo("write the report").Push()`, "mem"), func(bridge.SideRequest) string { return "report" })
	if !push.OK || push.IsError || len(push.Frames) != 1 {
		t.Fatalf("push: %+v", push)
	}
	c.result("c1", push)
	c.call("c2", `"looked around"`, "")
	c.result("c2", h.ask(runCall("c2", `"looked around"`, "")))

	c.call("c3", `mem.Pop("wrote report.md with the summary")`, "mem")
	pop := h.askSide(callIn(c, "c3", `mem.Pop("wrote report.md with the summary")`, "mem"), nil)
	if !pop.OK || !pop.IsError || len(pop.Frames) != 0 || !strings.Contains(textOf(core.Message{Blocks: pop.Content}), "report.md") {
		t.Fatalf("a close claiming a file nothing made: %+v", pop)
	}
	c.result("c3", pop)

	end := h.askSide(endIn(c, "The report is written."), nil)
	if !end.OK || end.Action != "continue" || end.Driver != "mem-close" || !strings.Contains(end.Send, "was refused, and it is still open") {
		t.Fatalf("end after a refused close: %+v", end)
	}
	end = h.askSide(endIn(c, "The report is written."), nil)
	if !end.OK || end.Action != "end" {
		t.Fatalf("the send-back is once a turn: %+v", end)
	}
	if code := h.close(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// The close review and the goal judge are asked of the host's model, with
// gnosh's own requests: a leaf's close is reviewed as it is made, and a
// top-level todo closed on its todos' results is judged as the turn would
// end; found not met, it is open again and the model is sent back with
// gnosh's words (goal.go judgeClosed). A pop's result says what left the
// model's context.
func TestBridgeReviewAndJudge(t *testing.T) {
	w := newWorld(t)
	h := w.bridge()
	h.ask(map[string]any{"op": "hello", "protocol": 1, "client": "test", "side": true})
	h.ask(map[string]any{"op": "prompt", "session": "S2", "first": true, "text": "ship the release"})

	asked := map[string]int{}
	var judged bridge.SideRequest
	answer := func(sr bridge.SideRequest) string {
		asked[sr.Purpose]++
		switch sr.Purpose {
		case "close-review":
			return `{"ok": true, "reason": "the test run shows it"}`
		case "goal-judge":
			judged = sr
			return `{"ok": false, "basis": "evidence", "reason": "nothing shows the release was published"}`
		}
		return "a-name"
	}
	c := &conv{session: "S2"}
	c.prompt("ship the release")
	step := func(id, code, vm string) bridge.Reply {
		c.call(id, code, vm)
		rep := h.askSide(callIn(c, id, code, vm), answer)
		if !rep.OK {
			t.Fatalf("%s: %+v", id, rep)
		}
		c.result(id, rep)
		return rep
	}
	step("c1", `mem.Todo("ship the release").Push()`, "mem")
	step("c2", `mem.Todo("run the tests").Push()`, "mem")
	step("c3", `"go test ./... ok"`, "")
	pop := step("c4", `mem.Pop("go test ./... passes")`, "mem")
	if pop.IsError || len(pop.Frames) != 1 || pop.Frames[0].Kind != "pop" {
		t.Fatalf("the reviewed pop: %+v", pop)
	}
	if got := textOf(core.Message{Blocks: pop.Content}); !strings.Contains(got, "was removed from your context") {
		t.Errorf("the pop's result does not say what left the context: %s", got)
	}
	if asked["close-review"] != 1 {
		t.Errorf("close reviews asked: %v", asked)
	}
	step("c5", `"published"`, "")
	if rep := step("c6", `mem.Pop("released")`, "mem"); rep.IsError {
		t.Fatalf("the parent's pop: %+v", rep)
	}

	end := h.askSide(endIn(c, "Released."), answer)
	if !end.OK || end.Action != "continue" || end.Driver != "mem-goal" || !strings.Contains(end.Send, "nothing shows the release was published") {
		t.Fatalf("end with a top todo the judge finds not met: %+v (asked %v)", end, asked)
	}
	if judged.System == "" || judged.MaxTokens != 300 || judged.Temperature == nil || *judged.Temperature != 0 || !strings.Contains(judged.Prompt, "ship the release") {
		t.Errorf("the judge's request: %+v", judged)
	}
	// Judged once: the same close is not judged again.
	end = h.askSide(endIn(c, "Released, really."), answer)
	if end.Action != "end" || asked["goal-judge"] != 1 {
		t.Fatalf("judged again: %+v %v", end, asked)
	}
	if code := h.close(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
