// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

//go:build nognovm && !windows

package gnovm

import (
	"errors"
	"fmt"
	"os"
)

// This file is what a gnosh built with -tags nognovm has in place of the
// GnoVM: it does not link the interpreter or embed its standard library,
// and runs its VMs only in gnosh-gnovm-worker (gnovm/cmd/gnosh-gnovm-worker),
// a program of its own. The files that make up the host carry the opposite
// build constraint. Windows keeps the host in-process for now, since it
// cannot yet hand a worker the protocol's pipes.

// errNoGnoVM is asking a build without the GnoVM to run VMs in-process.
var errNoGnoVM = errors.New("gnovm: this gnosh is built without the GnoVM (-tags nognovm), so it cannot run VMs in-process; they run in " + WorkerProgram)

// inProcess is never a host here: VMs run in the worker.
func (c *Client) inProcess() (localHost, error) {
	if !c.opts.InProcess {
		return nil, nil
	}
	return nil, errNoGnoVM
}

// selfIsWorker is false: this build is not a worker.
const selfIsWorker = false

// gnoVersion is "": the worker, which holds the interpreter, knows it.
func gnoVersion() string { return "" }

// WorkerMain refuses: this build does not contain the worker.
func WorkerMain() int {
	fmt.Fprintf(os.Stderr, "gnovm-worker: this gnosh is built without the GnoVM (-tags nognovm); its worker is %s\n", WorkerProgram)
	return 2
}
