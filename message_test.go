package agentrun

import (
	"reflect"
	"testing"
)

func TestTaskCounts_Pending(t *testing.T) {
	tests := []struct {
		name string
		tc   TaskCounts
		want int
	}{
		{name: "zero value", tc: TaskCounts{}, want: 0},
		{name: "started only", tc: TaskCounts{Started: 3}, want: 3},
		{name: "nets to zero", tc: TaskCounts{Started: 2, Finished: 2}, want: 0},
		{name: "outstanding", tc: TaskCounts{Started: 5, Finished: 2}, want: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tc.Pending(); got != tt.want {
				t.Errorf("Pending() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBackgroundStats_Kind(t *testing.T) {
	tests := []struct {
		name   string
		stats  *BackgroundStats
		lookup BackgroundKind
		wantOK bool
		wantTC TaskCounts
	}{
		{
			name:   "nil receiver is not tracked",
			stats:  nil,
			lookup: BackgroundSubagent,
			wantOK: false,
			wantTC: TaskCounts{},
		},
		{
			name:   "kind absent from Kinds is not tracked",
			stats:  &BackgroundStats{Kinds: []TaskCounts{{Kind: BackgroundSubagent, Started: 1}}},
			lookup: BackgroundShell,
			wantOK: false,
			wantTC: TaskCounts{},
		},
		{
			name:   "kind present, even when quiescent, is tracked",
			stats:  &BackgroundStats{Kinds: []TaskCounts{{Kind: BackgroundSubagent, Started: 2, Finished: 2}}},
			lookup: BackgroundSubagent,
			wantOK: true,
			wantTC: TaskCounts{Kind: BackgroundSubagent, Started: 2, Finished: 2},
		},
		{
			name: "multiple kinds tracked independently",
			stats: &BackgroundStats{Kinds: []TaskCounts{
				{Kind: BackgroundSubagent, Started: 1, Finished: 0},
				{Kind: BackgroundShell, Started: 4, Finished: 1},
			}},
			lookup: BackgroundShell,
			wantOK: true,
			wantTC: TaskCounts{Kind: BackgroundShell, Started: 4, Finished: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.stats.Kind(tt.lookup)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.wantTC) {
				t.Errorf("TaskCounts = %+v, want %+v", got, tt.wantTC)
			}
		})
	}
}

// TestBackgroundStats_AbsenceIsNotZero pins the core ADR-2 contract: an
// untracked kind (absent from Kinds) must be distinguishable from a tracked
// kind that happens to be quiescent ({0,0}). Both report Pending()==0 through
// the comma-ok result, but only the tracked one reports ok==true.
func TestBackgroundStats_AbsenceIsNotZero(t *testing.T) {
	stats := &BackgroundStats{Kinds: []TaskCounts{{Kind: BackgroundSubagent, Started: 0, Finished: 0}}}

	tracked, ok := stats.Kind(BackgroundSubagent)
	if !ok {
		t.Fatal("BackgroundSubagent should be tracked (present in Kinds)")
	}
	if tracked.Pending() != 0 {
		t.Errorf("tracked quiescent Pending() = %d, want 0", tracked.Pending())
	}

	untracked, ok := stats.Kind(BackgroundShell)
	if ok {
		t.Fatal("BackgroundShell should NOT be tracked (absent from Kinds)")
	}
	if untracked.Pending() != 0 {
		t.Errorf("zero-value TaskCounts.Pending() = %d, want 0 (still safe to call)", untracked.Pending())
	}
}

// TestBackgroundStats_NilStatsIsNotTrackedAtAll pins the outer nil-vs-absence
// layer: a nil *BackgroundStats (backend tracks nothing) must not be confused
// with a non-nil BackgroundStats whose Kinds happens to omit a kind.
func TestBackgroundStats_NilStatsIsNotTrackedAtAll(t *testing.T) {
	var stats *BackgroundStats
	if _, ok := stats.Kind(BackgroundSubagent); ok {
		t.Error("nil *BackgroundStats must report ok=false for any kind")
	}
}
