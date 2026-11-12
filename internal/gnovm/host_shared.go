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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Options configures a Host.
type Options struct {
	// Dir is the data directory, <XDG data>/gnosh/gnovm.
	Dir string
	// Client names the gnosh process the host serves, for its memory-only
	// VMs, which live until that process exits; NewClientID makes one.
	Client string
	// BudgetBytes caps the memory the VMs may use; 0 picks it from the
	// host's free memory.
	BudgetBytes int64
	// Defaults for new VMs; zero values mean the built-in defaults.
	DefaultMemoryMB int
	DefaultGas      int64
	NsPerGas        float64
	// Probe reads the host's memory; nil means ProbeHostMemory.
	Probe func() (HostMemory, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Embeddings is the embedding server mem finds by meaning with, an
	// OpenAI-compatible base URL such as http://127.0.0.1:8091/v1. mem's
	// text, and what the user types, are sent there. "" finds by words
	// alone, and sends nothing anywhere.
	Embeddings string
}

// NewClientID returns a fresh id for a gnosh process.
func NewClientID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(b[:])
}

// deleteEphemeral deletes a client's memory-only VMs, in every project.
func deleteEphemeral(dir, client string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "*", ".ephemeral", client))
	for _, m := range matches {
		_ = os.RemoveAll(m)
	}
}

// projectKey names a project's directory: its path, made safe, and a hash of
// it, so two projects never share one.
func projectKey(project string) string {
	clean := filepath.Clean(project)
	sum := sha256.Sum256([]byte(clean))
	var b strings.Builder
	for _, r := range strings.Trim(clean, string(filepath.Separator)) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 48 {
		s = s[len(s)-48:]
	}
	return s + "-" + hex.EncodeToString(sum[:4])
}

// ScratchName is the name of a session's scratch VM.
func ScratchName(session string) string {
	sum := sha256.Sum256([]byte(session))
	return scratchPrefix + hex.EncodeToString(sum[:4])
}

// busyError is a VM that cannot be used right now; trying again later may
// work. again marks mem's catch-up whose slice is spent: the work goes on
// with the next call, which the client makes at once, never the model.
type busyError struct {
	msg   string
	again bool
}

func (e *busyError) Error() string { return e.msg }

// scratchPrefix begins the name of a session's scratch VM (ScratchName).
const scratchPrefix = "scratch-"
