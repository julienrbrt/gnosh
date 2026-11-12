// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import "github.com/charmbracelet/crush/internal/version"

// myHello is this build's half of the handshake.
func myHello() hello {
	return hello{Magic: protocolMagic, Protocol: protocolVersion, Build: version.BuildID, Pinned: version.BuildIDPinned(), Gno: gnoVersion()}
}
