// Copyright (C) 2026 Jae Kwon
//
// This file is part of gnosh's GnoVM machine subsystem. It is licensed under
// the GNO Network General Public License, version 6, as published by
// NewTendermint, LLC or its successor, or any later version of that License
// that the Licensor accepts as the proxy under its section 14, by a statement
// published in the canonical copy of internal/gnovm/LICENSE.md; together with
// the Additional Terms (Strong Attribution) in that file.

package api

import "testing"

func TestBuildsAgree(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		a, b Hello
		want bool
	}{
		{"same build", Hello{Build: "x", Pinned: true}, Hello{Build: "x", Pinned: true}, true},
		{"two release builds", Hello{Build: "x", Pinned: true}, Hello{Build: "y", Pinned: true}, false},
		{"release and development", Hello{Build: "x", Pinned: true}, Hello{Build: "y"}, false},
		{"development and release", Hello{Build: "x"}, Hello{Build: "y", Pinned: true}, false},
		{"two development builds", Hello{Build: "x"}, Hello{Build: "y"}, true},
	} {
		if got := BuildsAgree(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: BuildsAgree = %v, want %v", tc.name, got, tc.want)
		}
	}
}
