package main

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestWaitBackgroundHonorsCancellation(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitBackground(ctx, &wg); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	wg.Done()
	if err := waitBackground(context.Background(), &wg); err != nil {
		t.Fatal(err)
	}
}
