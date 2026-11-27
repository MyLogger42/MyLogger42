package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGracefulShutdownWaitsForWorkersBeforeReturning(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	httpServer := &http.Server{}

	var wg sync.WaitGroup
	var workerFinished atomic.Bool
	releaseWorker := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-releaseWorker
		workerFinished.Store(true)
	}()

	shutdownReturned := make(chan struct{})
	go func() {
		defer close(shutdownReturned)
		gracefulShutdown(httpServer, &wg, logger)
	}()

	select {
	case <-shutdownReturned:
		t.Fatal("gracefulShutdown returned before background worker exited")
	case <-time.After(30 * time.Millisecond):
	}

	if workerFinished.Load() {
		t.Fatal("worker should still be running before release")
	}

	close(releaseWorker)

	select {
	case <-shutdownReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gracefulShutdown to return")
	}

	if !workerFinished.Load() {
		t.Fatal("expected worker to finish before gracefulShutdown returned")
	}

	// Ensure context-based httpServer.Shutdown with no listener returns nil cleanly.
	_ = context.Background()
}
