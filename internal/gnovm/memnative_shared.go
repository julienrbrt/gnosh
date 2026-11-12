// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

// memOp is one change to a mem, as gnomem's do hands it to gnosh and the
// journal keeps it (gnomem's Op).
type memOp struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Field string `json:"field,omitempty"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
	By    string `json:"by,omitempty"`
}

// stackAfter is stack, a session's frames bottom to top, after events, a
// call's pushes and pops in order: a push goes on top, and a pop takes its
// frame off.
func stackAfter(stack []string, events []MemFrame) []string {
	out := append([]string(nil), stack...)
	for _, e := range events {
		switch e.Kind {
		case "push":
			out = append(out, e.Id)
		case "pop":
			for i := len(out) - 1; i >= 0; i-- {
				if out[i] == e.Id {
					out = append(out[:i:i], out[i+1:]...)
					break
				}
			}
		}
	}
	return out
}
