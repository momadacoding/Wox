package plugin

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordedSelections struct {
	mu  sync.Mutex
	ids []string
}

func (r *recordedSelections) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
}

func (r *recordedSelections) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

// newRecordingSelectionDispatcher resolves every result to a callback that records its id.
func newRecordingSelectionDispatcher(delay time.Duration) (*resultSelectionDispatcher, *recordedSelections) {
	recorded := &recordedSelections{}
	dispatcher := newResultSelectionDispatcher(delay, func(sessionID, queryID, resultID string) (func(context.Context), bool) {
		return func(context.Context) { recorded.add(queryID + "/" + resultID) }, true
	})
	return dispatcher, recorded
}

func waitForSelections(t *testing.T, recorded *recordedSelections, want int) []string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := recorded.snapshot(); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return recorded.snapshot()
}

func TestResultSelectionDispatchesOnlyTheSettledRow(t *testing.T) {
	dispatcher, recorded := newRecordingSelectionDispatcher(20 * time.Millisecond)
	ctx := context.Background()

	dispatcher.selected(ctx, "session", "q1", "a")
	dispatcher.selected(ctx, "session", "q1", "b")
	dispatcher.selected(ctx, "session", "q1", "c")

	got := waitForSelections(t, recorded, 1)
	time.Sleep(60 * time.Millisecond)
	got = recorded.snapshot()
	if len(got) != 1 || got[0] != "q1/c" {
		t.Fatalf("expected only the settled row q1/c, got %v", got)
	}
}

func TestResultSelectionIgnoresRepeatedNotificationsForSameRow(t *testing.T) {
	dispatcher, recorded := newRecordingSelectionDispatcher(10 * time.Millisecond)
	ctx := context.Background()

	dispatcher.selected(ctx, "session", "q1", "a")
	waitForSelections(t, recorded, 1)
	// Result list refreshes re-announce the unchanged selection.
	dispatcher.selected(ctx, "session", "q1", "a")
	time.Sleep(40 * time.Millisecond)

	if got := recorded.snapshot(); len(got) != 1 {
		t.Fatalf("expected one dispatch for an unchanged row, got %v", got)
	}
}

func TestResultSelectionClearingCancelsPendingDispatch(t *testing.T) {
	dispatcher, recorded := newRecordingSelectionDispatcher(20 * time.Millisecond)
	ctx := context.Background()

	dispatcher.selected(ctx, "session", "q1", "a")
	dispatcher.selected(ctx, "session", "q1", "")
	dispatcher.selected(ctx, "other", "q2", "b")
	dispatcher.clear("other")
	time.Sleep(60 * time.Millisecond)

	if got := recorded.snapshot(); len(got) != 0 {
		t.Fatalf("expected no dispatch after the selection was cleared, got %v", got)
	}
}

func TestResultSelectionReselectingAfterClearDispatchesAgain(t *testing.T) {
	dispatcher, recorded := newRecordingSelectionDispatcher(10 * time.Millisecond)
	ctx := context.Background()

	dispatcher.selected(ctx, "session", "q1", "a")
	waitForSelections(t, recorded, 1)
	dispatcher.selected(ctx, "session", "q1", "")
	dispatcher.selected(ctx, "session", "q1", "a")

	if got := waitForSelections(t, recorded, 2); len(got) != 2 {
		t.Fatalf("expected the row to dispatch again after it was deselected, got %v", got)
	}
}
