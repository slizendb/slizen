package proxy

import (
	"sync/atomic"
	"testing"
)

func BenchmarkDrainTrackerHandler(b *testing.B) {
	tracker := newDrainTracker()

	b.ReportAllocs()
	for b.Loop() {
		if !tracker.beginHandler() {
			b.Fatal("handler admission unexpectedly closed")
		}
		draining, err := tracker.prepareHandlerDone(nil, 0, 0)
		if draining || err != nil {
			b.Fatalf("handler completion = draining %t, error %v", draining, err)
		}
	}

	if active, connections := tracker.snapshot(); active != 0 || connections != 0 {
		b.Fatalf("final drain state = active:%d connections:%d, want 0/0", active, connections)
	}
}

func BenchmarkDrainTrackerHandlerParallel(b *testing.B) {
	tracker := newDrainTracker()
	var failed atomic.Bool

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if !tracker.beginHandler() {
				failed.Store(true)
				return
			}
			draining, err := tracker.prepareHandlerDone(nil, 0, 0)
			if draining || err != nil {
				failed.Store(true)
				return
			}
		}
	})

	if failed.Load() {
		b.Fatal("handler accounting failed during parallel benchmark")
	}
	if active, connections := tracker.snapshot(); active != 0 || connections != 0 {
		b.Fatalf("final drain state = active:%d connections:%d, want 0/0", active, connections)
	}
}
