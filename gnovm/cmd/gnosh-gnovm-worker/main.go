// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

// Command gnosh-gnovm-worker is gnosh's GnoVM worker as a program of its
// own: the process that runs one gnosh's VMs, speaking the worker protocol
// (internal/gnovm/PROTOCOL.md) on fds 3 and 4. gnosh starts it when it is
// installed next to gnosh's executable; it does nothing without a gnosh
// client's handshake. It is the same worker as `gnosh gnovm-worker`.
package main

import (
	"os"

	"github.com/charmbracelet/crush/internal/gnovm"
)

func main() {
	os.Exit(gnovm.WorkerMain())
}
