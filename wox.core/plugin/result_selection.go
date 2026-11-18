package plugin

import (
	"context"
	"time"
	"wox/util"
)

// resultSelectedDelay lets arrow-key scrolling settle before plugins act on the selected row,
// so a callback that drives another app (for example, selecting the file in Explorer) runs
// once for the row the user stops on instead of once per intermediate row.
const resultSelectedDelay = 100 * time.Millisecond

// resultSelectionState is the newest selection announced by one launcher session.
// timer is nil when the selection was cleared or the callback has already been scheduled away.
type resultSelectionState struct {
	queryID  string
	resultID string
	timer    *time.Timer
}

// resultSelectionDispatcher debounces QueryResult.OnSelected per launcher session.
type resultSelectionDispatcher struct {
	delay    time.Duration
	sessions *util.HashMap[string, *resultSelectionState]
	// resolve looks up the callback when the timer fires, so a result dropped from the
	// cache in the meantime is simply skipped.
	resolve func(sessionID, queryID, resultID string) (func(context.Context), bool)
}

// newResultSelectionDispatcher creates a dispatcher with an injectable resolver for focused tests.
func newResultSelectionDispatcher(delay time.Duration, resolve func(sessionID, queryID, resultID string) (func(context.Context), bool)) *resultSelectionDispatcher {
	return &resultSelectionDispatcher{
		delay:    delay,
		sessions: util.NewHashMap[string, *resultSelectionState](),
		resolve:  resolve,
	}
}

// selected records the session's selected row and schedules its callback. The launcher
// re-announces the unchanged selection after every result refresh; those repeats keep the
// already scheduled or delivered dispatch instead of firing again.
func (d *resultSelectionDispatcher) selected(ctx context.Context, sessionID, queryID, resultID string) {
	if sessionID == "" {
		return
	}
	d.sessions.Compute(sessionID, func(state *resultSelectionState, exists bool) (*resultSelectionState, bool) {
		if exists && state.queryID == queryID && state.resultID == resultID {
			return state, true
		}
		if exists && state.timer != nil {
			state.timer.Stop()
		}
		next := &resultSelectionState{queryID: queryID, resultID: resultID}
		if resultID != "" {
			next.timer = time.AfterFunc(d.delay, func() { d.fire(ctx, sessionID, next) })
		}
		return next, true
	})
}

// fire runs the callback only if the row is still the session's newest selection.
func (d *resultSelectionDispatcher) fire(ctx context.Context, sessionID string, state *resultSelectionState) {
	if current, ok := d.sessions.Load(sessionID); !ok || current != state {
		return
	}
	onSelected, ok := d.resolve(sessionID, state.queryID, state.resultID)
	if !ok || onSelected == nil {
		return
	}
	util.Go(ctx, "result selected callback", func() {
		onSelected(ctx)
	})
}

// clear cancels a pending dispatch when the launcher session is destroyed.
func (d *resultSelectionDispatcher) clear(sessionID string) {
	d.sessions.Compute(sessionID, func(state *resultSelectionState, exists bool) (*resultSelectionState, bool) {
		if exists && state.timer != nil {
			state.timer.Stop()
		}
		return nil, false
	})
}

// ResultSelected schedules the selected result's OnSelected callback for one launcher session.
func (m *Manager) ResultSelected(ctx context.Context, sessionID, queryID, resultID string) {
	m.resultSelections.selected(ctx, sessionID, queryID, resultID)
}

// resolveResultSelected returns the cached result's OnSelected callback, if it still exists.
func (m *Manager) resolveResultSelected(sessionID, queryID, resultID string) (func(context.Context), bool) {
	resultCache, found := m.resultStore.findInSession(sessionID, queryID, resultID)
	if !found || resultCache == nil || resultCache.Result.OnSelected == nil {
		return nil, false
	}
	return resultCache.Result.OnSelected, true
}
