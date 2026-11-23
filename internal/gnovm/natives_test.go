// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package gnovm

import (
	"context"
	"testing"
	"time"

	gno "github.com/gnolang/gno/gnovm/pkg/gnolang"
	"github.com/stretchr/testify/require"
)

// A fmt or mem native looks at the call's wall clock, and for a stop,
// before it does anything: each costs a flat amount of gas whatever it
// does, and the meter looked only every million gas, so a loop of Finds
// over a big mem ran for minutes past its limit, holding mem's lock (the
// gno review of 2026-11-19).
func TestNativesLookAtTheClockFirst(t *testing.T) {
	for _, n := range []struct {
		pkg  string
		name gno.Name
	}{{"fmt", "sprint"}, {"fmt", "sprintf"}, {memLibPath, "find"}, {memLibPath, "record"}} {
		f := nativeResolver(n.pkg, n.name)
		require.NotNil(t, f, n.name)
		meter := &callMeter{}
		meter.begin(context.Background(), 1<<40, time.Now().Add(-time.Second))
		func() {
			defer func() {
				stop, ok := recover().(stopRun)
				require.True(t, ok, "%s.%s did not stop at its deadline first", n.pkg, n.name)
				require.ErrorIs(t, stop.err, errWallClock)
			}()
			f(&gno.Machine{GasMeter: meter})
		}()
	}
}
