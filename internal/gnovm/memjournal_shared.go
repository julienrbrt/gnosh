// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

// opTarget is what an Op names: an "item", "fact" or "rule"; or
// "statements", for a redact of past inputs' code alone.
func opTarget(op memOp) string {
	switch {
	case op.Kind == "fact" || op.Kind == "rule":
		return op.Kind
	case op.Kind == "redact", op.Kind == "forget" && op.Field != "":
		return op.Field
	}
	return "item"
}
