package hotness

import "sort"

func sortSnapshots(snapshots []Snapshot) {
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].State == StateHot && snapshots[j].State != StateHot {
			return true
		}
		if snapshots[i].State != StateHot && snapshots[j].State == StateHot {
			return false
		}
		if snapshots[i].Score == snapshots[j].Score {
			return snapshots[i].Key < snapshots[j].Key
		}
		return snapshots[i].Score > snapshots[j].Score
	})
}
