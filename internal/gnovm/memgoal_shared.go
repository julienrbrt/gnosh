// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import "fmt"

// GoalCheckpoint is what every tenth continuation of a goal asks instead
// of going on: whether there is a real reason to stop. There is no cap.
func GoalCheckpoint(b GoalBinding, continued int, marker string) string {
	return fmt.Sprintf("You have been sent on toward the goal `%[1]s` %[2]q %[3]d times. Step back: is there a real reason "+
		"to stop? For example, the goal cannot be reached as set; it is blocked on something only the user can give or "+
		"decide; or you are repeating the same attempts without progress. If the goal already holds, show it and close it "+
		"with `mem.Done(%[1]q, \"<command> → <result>\")`. If there is a reason to stop, explain it for the user, then end "+
		"your reply with this line on its own:\n%[4]s\nOtherwise keep working: many attempts are fine.",
		b.Name, memLine(b.Title, 80), continued, marker)
}
