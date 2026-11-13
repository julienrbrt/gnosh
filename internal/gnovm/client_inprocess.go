// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

//go:build !nognovm || windows

package gnovm

// inProcess returns the client's own host when it runs VMs in-process, made
// on first use, or nil when it uses a worker.
func (c *Client) inProcess() (localHost, error) {
	if !c.opts.InProcess {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.host == nil {
		h, err := NewHost(c.opts.Options)
		if err != nil {
			return nil, err
		}
		c.host = h
	}
	return c.host, nil
}

// selfIsWorker reports whether this build can be its own worker, as
// `gnosh gnovm-worker`: it contains the GnoVM.
const selfIsWorker = true

// gnoVersion is the gno version of the interpreter this build contains.
func gnoVersion() string { return stdlibVersion() }
