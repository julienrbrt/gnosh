//go:build !linux

package sandbox

import (
	"fmt"
	"os"
)

// FilterMain refuses: the filter is Linux's.
func FilterMain(args []string) int {
	fmt.Fprintln(os.Stderr, "gnosh sandbox-filter: the filter is for Linux")
	return 2
}

// notDumpable is Linux's.
func notDumpable() {}

// canFilter reports that there is no filter here.
func canFilter() bool { return false }
