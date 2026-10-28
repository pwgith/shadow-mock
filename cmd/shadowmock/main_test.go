package main

import (
	"bytes"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noEnv(string) (string, bool) { return "", false }

func TestRun(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run([]string{"-version"}, noEnv, &out); err != nil || !strings.Contains(out.String(), version) {
		t.Errorf("run(-version) = %v, %q", err, out.String())
	}
	if err := run([]string{"-nope"}, noEnv, &out); err == nil {
		t.Error("an unknown flag should fail")
	}
	if err := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, noEnv, &out); err == nil {
		t.Error("a missing config file should fail")
	}
}

// dial opens a client connection to ln and closes it when the test ends.
func dial(t *testing.T, ln net.Listener) {
	t.Helper()
	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
}

// acceptAll sends every connection the listener accepts down the channel, and closes it with the test.
func acceptAll(t *testing.T, ln net.Listener) <-chan net.Conn {
	t.Helper()
	accepted := make(chan net.Conn, 10)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
			accepted <- c
		}
	}()
	return accepted
}

// A listener with a limit holds that many connections; another waits until one closes.
func TestListenHoldsAtMostTheLimitedNumberOfConnections(t *testing.T) {
	t.Parallel()
	ln, err := listen("127.0.0.1:0", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := acceptAll(t, ln)
	for range 3 {
		dial(t, ln)
	}
	first := make([]net.Conn, 0, 2)
	for range 2 {
		select {
		case c := <-accepted:
			first = append(first, c)
		case <-time.After(2 * time.Second):
			t.Fatal("the first two connections should be accepted")
		}
	}
	select {
	case <-accepted:
		t.Fatal("a third connection was accepted while two were open")
	case <-time.After(300 * time.Millisecond):
	}
	_ = first[0].Close()
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("the third connection should be accepted once one closed")
	}
}

func TestListenWithNoLimitAcceptsEveryConnection(t *testing.T) {
	t.Parallel()
	ln, err := listen("127.0.0.1:0", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := acceptAll(t, ln)
	for range 5 {
		dial(t, ln)
	}
	for i := range 5 {
		select {
		case <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 5 connections were accepted", i)
		}
	}
}

func TestListenReportsAnAddressThatCannotBeUsed(t *testing.T) {
	t.Parallel()
	if _, err := listen("not-an-address", 0); err == nil {
		t.Error("a bad address should be an error")
	}
}
