// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

// memStamp is what a store holds, kept in it under memStampKey and written
// in the batch of each change: the layout and the gno version it was built
// under, and the journal's generation and the entry it holds, with where
// that entry's line ends and the line's hash.
type memStamp struct {
	Layout     int    `json:"layout"`
	Gno        string `json:"gno"`
	Generation string `json:"generation"`
	Seq        int64  `json:"seq"`
	End        int64  `json:"end"`
	Hash       string `json:"hash,omitempty"`
	// Unchecked marks a store being built afresh from the journal, whose
	// rebuild has not been checked yet (memHost.checkRebuild): one cut
	// across calls is checked by the call that finishes it.
	Unchecked bool `json:"unchecked,omitempty"`
}
