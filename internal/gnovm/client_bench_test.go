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
	"os"
	"testing"
)

func benchClient(b *testing.B, inProcess bool) *Client {
	b.Helper()
	opts := ClientOptions{Options: Options{Dir: b.TempDir()}, InProcess: inProcess}
	if !inProcess {
		bin := os.Getenv("GNOVM_WORKER_BIN")
		if bin == "" {
			b.Skip("set GNOVM_WORKER_BIN to a gnosh binary built from this tree, with the same version.BuildID as this test")
		}
		opts.Binary = bin
	}
	c, err := Acquire(opts)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(c.Release)
	return c
}

func BenchmarkClientRunInProcess(b *testing.B) {
	benchmarkClientRun(b, benchClient(b, true))
}

func BenchmarkClientRunWorker(b *testing.B) {
	benchmarkClientRun(b, benchClient(b, false))
}

func benchmarkClientRun(b *testing.B, c *Client) {
	ctx := context.Background()
	req := RunRequest{Project: "/p", Session: "s", Code: "1 + 1"}
	res, err := c.Run(ctx, req)
	if err != nil {
		b.Fatal(err)
	}
	if res.Err != nil {
		b.Fatal(res.Err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := c.Run(ctx, req)
		if err != nil {
			b.Fatal(err)
		}
		if res.Err != nil {
			b.Fatal(res.Err)
		}
	}
}

func BenchmarkHostMemRead(b *testing.B) {
	dir := b.TempDir()
	h, err := NewHost(Options{Dir: dir, Client: NewClientID()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = h.Close() })
	ctx := context.Background()
	if _, err := h.Run(ctx, RunRequest{Project: "/p", Session: "s1", Name: MemVM, Code: `mem.Todo("ship the release")`}); err != nil {
		b.Fatal(err)
	}
	benchmarkMemRead(b, func() (string, bool, error) {
		return h.MemRead(ctx, "/p", "gnosh mem", "", nil)
	})
}

func BenchmarkClientMemReadInProcess(b *testing.B) {
	c := benchClient(b, true)
	ctx := context.Background()
	if _, err := c.Run(ctx, RunRequest{Project: "/p", Session: "s1", Name: MemVM, Code: `mem.Todo("ship the release")`}); err != nil {
		b.Fatal(err)
	}
	benchmarkMemRead(b, func() (string, bool, error) {
		return c.MemRead(ctx, "/p", "gnosh mem", "", nil)
	})
}

func BenchmarkClientMemReadWorker(b *testing.B) {
	c := benchClient(b, false)
	ctx := context.Background()
	if _, err := c.Run(ctx, RunRequest{Project: "/p", Session: "s1", Name: MemVM, Code: `mem.Todo("ship the release")`}); err != nil {
		b.Fatal(err)
	}
	benchmarkMemRead(b, func() (string, bool, error) {
		return c.MemRead(ctx, "/p", "gnosh mem", "", nil)
	})
}

func benchmarkMemRead(b *testing.B, read func() (string, bool, error)) {
	text, refused, err := read()
	if err != nil {
		b.Fatal(err)
	}
	if refused {
		b.Fatal(text)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		text, refused, err := read()
		if err != nil {
			b.Fatal(err)
		}
		if refused {
			b.Fatal(text)
		}
	}
}
