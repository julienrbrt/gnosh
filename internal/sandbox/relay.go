package sandbox

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
)

// RelayWord is the first word that makes gnosh, started inside the Linux
// sandbox, the relay to the proxy outside it (RelayMain): gnosh itself,
// so that nothing but bubblewrap need be installed.
const RelayWord = "sandbox-relay"

// relayListen is where the relay listens inside the sandbox, on its own
// loopback, and what the command is told its proxy is.
const relayListen = "127.0.0.1:3128"

// RelayMain is gnosh run as "sandbox-relay <socket> <program> <args...>"
// inside the sandbox: it listens on the sandbox's own loopback, carries
// each connection to the proxy's socket (made visible inside the
// sandbox), runs the program while it does, and returns the program's
// exit status.
func RelayMain(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "gnosh sandbox-relay: a proxy's socket and a program to run")
		return 2
	}
	socket, argv := args[0], args[1:]
	// (The relay makes Unix sockets, which the command may not: it is
	// not to be attached to and made to make one for it.)
	notDumpable()
	ln, err := net.Listen("tcp", relayListen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gnosh: the sandbox's way to its proxy could not be opened (%v)\n", err)
		return 126
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				up, err := net.Dial("unix", socket)
				if err != nil {
					c.Close()
					return
				}
				pipe(c, up)
			}()
		}
	}()
	path, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gnosh: %v\n", err)
		return 127
	}
	// (With the filter on it is the sandbox's first process, its init:
	// bubblewrap's own, which a command could attach to, is not there.)
	return runChild(path, argv)
}

// pipe copies between two connections until both are done.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); closeWrite(a); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); closeWrite(b); done <- struct{}{} }()
	<-done
	<-done
	a.Close()
	b.Close()
}

// proxyURL is the proxy as the command is told it.
func proxyURL(p Policy) string {
	if p.Proxy == "" {
		return ""
	}
	if strings.HasPrefix(p.Proxy, "/") {
		// (Linux: the relay inside the sandbox.)
		return "http://" + relayListen
	}
	return "http://" + p.Proxy
}
