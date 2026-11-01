package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freeAddr returns a loopback address nothing is listening on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)
}

// get is called from helper goroutines, so it reports errors instead of
// failing the test itself.
func get(url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// A request in flight when SIGTERM arrives must finish, and the DB/MQ must stay
// open until it has: cleanup runs only after the drain.
func TestServeDrainsInFlightRequestsBeforeCleanup(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}

	started := make(chan struct{})
	var startOnce sync.Once
	release := make(chan struct{})
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		startOnce.Do(func() { close(started) })
		<-release
		record("request done")
		_, _ = io.WriteString(w, "ok")
	})}

	stop := make(chan os.Signal, 1)
	served := make(chan error, 1)
	go func() { served <- serve(srv, stop, 5*time.Second, func() { record("cleanup") }) }()
	waitListening(t, addr)

	type result struct {
		body string
		err  error
	}
	responded := make(chan result, 1)
	go func() {
		resp, err := get("http://" + addr + "/")
		if err != nil {
			responded <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		responded <- result{body: string(body), err: err}
	}()
	<-started

	stop <- syscall.SIGTERM
	// Still draining: serve must not have returned, and cleanup must not have run.
	select {
	case err := <-served:
		t.Fatalf("serve returned while a request was in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	mu.Lock()
	assert.Empty(t, events, "cleanup ran before the in-flight request finished")
	mu.Unlock()

	close(release)

	r := <-responded
	require.NoError(t, r.err)
	assert.Equal(t, "ok", r.body)
	require.NoError(t, <-served)
	assert.Equal(t, []string{"request done", "cleanup"}, events)
}

// A request that never finishes can't hold shutdown past the timeout: serve
// gives up, still runs cleanup, and returns.
func TestServeGivesUpDrainingAfterTimeout(t *testing.T) {
	started := make(chan struct{})
	var startOnce sync.Once
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		startOnce.Do(func() { close(started) })
		<-release
	})}

	cleaned := make(chan struct{})
	stop := make(chan os.Signal, 1)
	served := make(chan error, 1)
	go func() { served <- serve(srv, stop, 100*time.Millisecond, func() { close(cleaned) }) }()
	waitListening(t, addr)

	go func() {
		if resp, err := get("http://" + addr + "/"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started

	stop <- syscall.SIGTERM
	select {
	case err := <-served:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the drain timeout")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("cleanup did not run")
	}
}

// If the port can't be bound, serve reports it instead of waiting forever for
// a signal that would drain a server that never started.
func TestServeReturnsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	srv := &http.Server{Addr: ln.Addr().String(), Handler: http.NotFoundHandler()}
	err = serve(srv, make(chan os.Signal), time.Second, func() { t.Error("cleanup must not run when the server never started") })
	require.Error(t, err)
}
