package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

type ProgressTracker struct {
	downloaded int64
	totalSize  int64
	done       chan struct{}
}

func (pt *ProgressTracker) Add(n int64) {
	atomic.AddInt64(&pt.downloaded, n)
}

func (pt *ProgressTracker) Run() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			downloaded := atomic.LoadInt64(&pt.downloaded)
			percent := float64(downloaded) / float64(pt.totalSize) * 100
			fmt.Printf("\rProgress: %.2f%% (%d/%d MB)", percent, downloaded/1024/1024, pt.totalSize/1024/1024)
		case <-pt.done:
			// final update when Finish is called
			downloaded := atomic.LoadInt64(&pt.downloaded)
			percent := float64(downloaded) / float64(pt.totalSize) * 100
			fmt.Printf("\rProgress: %.2f%% (%d/%d MB) ✅\n", percent, downloaded/1024/1024, pt.totalSize/1024/1024)
			return
		}
	}
}

func (pt *ProgressTracker) Finish() {
	close(pt.done)
}
