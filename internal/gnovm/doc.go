// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

// Package gnovm runs named Gno virtual machines for gnosh: each with a
// memory cap and a gas limit per call, its state kept as a journal of the
// calls that succeeded, persisted on request, and evicted from memory when
// the budget needs the room. DESIGN.md has the design and its reviews.
//
// A gnosh built with -tags nognovm holds only the client: the files that
// make up the host, which link the Gno interpreter and embed its standard
// library, carry the build constraint !nognovm, and the VMs run in
// gnosh-gnovm-worker (gnovm/cmd/gnosh-gnovm-worker). See DESIGN.md §14.
package gnovm
