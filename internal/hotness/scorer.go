package hotness

import (
	"container/heap"
	"sort"
	"time"
)

type worstSnapshotHeap []Snapshot

func (h worstSnapshotHeap) Len() int { return len(h) }

func (h worstSnapshotHeap) Less(i, j int) bool {
	return snapshotBetter(h[j], h[i])
}

func (h worstSnapshotHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *worstSnapshotHeap) Push(value any) {
	*h = append(*h, value.(Snapshot))
}

func (h *worstSnapshotHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

func sortSnapshots(snapshots []Snapshot) {
	sort.Slice(snapshots, func(i, j int) bool { return snapshotBetter(snapshots[i], snapshots[j]) })
}

func snapshotBetter(left, right Snapshot) bool {
	if left.State == StateHot && right.State != StateHot {
		return true
	}
	if left.State != StateHot && right.State == StateHot {
		return false
	}
	if left.Score == right.Score {
		return left.Key < right.Key
	}
	return left.Score > right.Score
}

func topSnapshots(items map[string]*entry, now time.Time, limit int) []Snapshot {
	if limit <= 0 || limit >= len(items) {
		out := make([]Snapshot, 0, len(items))
		for _, ent := range items {
			out = append(out, ent.snapshot(now))
		}
		sortSnapshots(out)
		return out
	}

	selected := make(worstSnapshotHeap, 0, limit)
	for _, ent := range items {
		snapshot := ent.snapshot(now)
		if len(selected) < limit {
			heap.Push(&selected, snapshot)
			continue
		}
		if snapshotBetter(snapshot, selected[0]) {
			selected[0] = snapshot
			heap.Fix(&selected, 0)
		}
	}
	out := []Snapshot(selected)
	sortSnapshots(out)
	return out
}
