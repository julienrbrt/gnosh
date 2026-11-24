package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHostDrivers(t *testing.T) {
	ctx := context.Background()
	calls := 0
	h := &HostDrivers{Drivers: []Driver{
		{Name: "first", Every: 3, Decide: func(context.Context, TurnEnd) (Verdict, error) {
			calls++
			return Verdict{Action: Continue, Prompt: "go on", Objection: "not done"}, nil
		}},
		{Name: "never", Decide: func(context.Context, TurnEnd) (Verdict, error) {
			t.Fatal("a later driver is asked after an earlier one sent the model back")
			return Verdict{}, nil
		}},
	}}
	h.Begin("t1")
	for i := 1; i <= 2; i++ {
		v := h.End(ctx, Reply{Text: "done"}, true)
		if !v.Continue || v.Prompt != "go on" || v.Driver != "first" || v.Objection != "not done" {
			t.Fatalf("send-back %d: %+v", i, v)
		}
	}
	v := h.End(ctx, Reply{Text: "done"}, true)
	if !v.Continue || !strings.Contains(v.Prompt, StopMarker) || !strings.HasSuffix(v.Prompt, "go on") {
		t.Fatalf("the third send-back is a checkpoint: %+v", v)
	}
	v = h.End(ctx, Reply{Text: "nothing is left to do\n" + StopMarker}, true)
	if v.Continue || v.Stopped != "nothing is left to do" || v.Driver != "first" || len(v.Notices) != 1 {
		t.Fatalf("stopping at the checkpoint: %+v", v)
	}
	if calls != 3 {
		t.Fatalf("the driver is not asked when the model stops at its checkpoint: %d calls", calls)
	}

	h = &HostDrivers{Drivers: []Driver{
		{Name: "broken", Decide: func(context.Context, TurnEnd) (Verdict, error) {
			return Verdict{}, errors.New("no model")
		}},
	}}
	h.Begin("t2")
	v = h.End(ctx, Reply{Text: "done"}, true)
	if !v.Continue || v.Driver != "broken" || len(v.Notices) != 1 || !strings.Contains(v.Prompt, "no model") {
		t.Fatalf("a driver that cannot decide sends the model back: %+v", v)
	}

	h = &HostDrivers{Drivers: []Driver{
		{Name: "fine", Decide: func(context.Context, TurnEnd) (Verdict, error) {
			return Verdict{Action: End}, nil
		}},
	}}
	h.Begin("t3")
	if v := h.End(ctx, Reply{Text: "done"}, true); v.Continue {
		t.Fatalf("the turn ends when no driver objects: %+v", v)
	}
}
