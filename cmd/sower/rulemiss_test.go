package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRuleMissTrackerCountsPerDomain(t *testing.T) {
	tracker := newRuleMissTracker()

	tracker.OnMiss("a.example.com")
	tracker.OnMiss("b.example.com")
	tracker.OnMiss("b.example.com")
	tracker.OnMiss("c.example.com")

	top := tracker.Top(10)
	if len(top) != 3 {
		t.Fatalf("expected 3 domains, got %d", len(top))
	}
	if top[0].Rule != "b.example.com" || top[0].Count != 2 {
		t.Fatalf("unexpected top: %+v", top[0])
	}
	// a and c tie at count 1; the tiebreak is most recent access, so c wins.
	if top[1].Rule != "c.example.com" || top[2].Rule != "a.example.com" {
		t.Fatalf("unexpected tie order: %+v", top)
	}
}

func TestRuleMissTrackerRecent(t *testing.T) {
	tracker := newRuleMissTracker()

	tracker.OnMiss("first.example")
	time.Sleep(2 * time.Millisecond)
	tracker.OnMiss("second.example")
	tracker.OnMiss("second.example")

	recent := tracker.Recent(10)
	if len(recent) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(recent))
	}
	if recent[0].Rule != "second.example" {
		t.Fatalf("expected most recent first, got %+v", recent[0])
	}
	if recent[1].Rule != "first.example" {
		t.Fatalf("expected older domain second, got %+v", recent[1])
	}
}

func TestRuleMissTrackerLimitAndTopCap(t *testing.T) {
	tracker := newRuleMissTracker()
	for i := 0; i < 5; i++ {
		tracker.OnMiss("x.example")
	}
	tracker.OnMiss("y.example")

	if got := len(tracker.Top(1)); got != 1 {
		t.Fatalf("expected limit 1, got %d", got)
	}
	if got := len(tracker.Top(0)); got != 2 {
		t.Fatalf("expected unlimited default, got %d", got)
	}
}

func TestRuleMissTrackerCapEvictsOldest(t *testing.T) {
	tracker := newRuleMissTracker()

	// Fill every shard to its limit so the next insert must evict.
	now := time.Now()
	for i := range tracker.shards {
		s := &tracker.shards[i]
		s.mu.Lock()
		for j := 0; j < s.limit; j++ {
			s.miss[fmt.Sprintf("domain-%d-%d.example", i, j)] = &ruleMiss{count: 1, last: now.Add(time.Duration(j) * time.Nanosecond)}
		}
		s.mu.Unlock()
	}

	tracker.OnMiss("fresh.example")

	idx := missShardIndex("fresh.example")
	s := &tracker.shards[idx]
	s.mu.Lock()
	n := len(s.miss)
	_, oldestPresent := s.miss[fmt.Sprintf("domain-%d-0.example", idx)]
	_, freshPresent := s.miss["fresh.example"]
	s.mu.Unlock()
	if n != missShardLimit {
		t.Fatalf("expected %d domains after eviction, got %d", missShardLimit, n)
	}
	if oldestPresent || !freshPresent {
		t.Fatalf("unexpected eviction: oldest=%v fresh=%v", oldestPresent, freshPresent)
	}
}

func TestRuleMissTrackerConcurrentSnapshot(t *testing.T) {
	tracker := newRuleMissTracker()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				tracker.OnMiss(fmt.Sprintf("host%d.example.com", i%64))
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = tracker.Top(10)
				_ = tracker.Recent(10)
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}
