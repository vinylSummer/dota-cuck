package sessions

import (
	"errors"
	"testing"
)

func TestValidTransitions(t *testing.T) {
	cases := []struct {
		from State
		ev   Event
		want State
	}{
		{StateOff, EventStart, StateStarting},
		{StateStarting, EventStreamStarted, StateWatching},
		{StateStarting, EventStop, StateStopping},
		{StateStarting, EventFatalError, StateStopping},
		{StateWatching, EventStop, StateStopping},
		{StateWatching, EventFatalError, StateStopping},
		{StateStopping, EventWorkerIdle, StateOff},
	}
	for _, c := range cases {
		got, err := Next(c.from, c.ev)
		if err != nil {
			t.Errorf("Next(%s, %s) unexpected error: %v", c.from, c.ev, err)
			continue
		}
		if got != c.want {
			t.Errorf("Next(%s, %s) = %s, want %s", c.from, c.ev, got, c.want)
		}
	}
}

func TestInvalidTransitionsRejected(t *testing.T) {
	valid := map[State]map[Event]bool{}
	for from, evs := range transitions {
		valid[from] = map[Event]bool{}
		for ev := range evs {
			valid[from][ev] = true
		}
	}

	allStates := []State{StateOff, StateStarting, StateWatching, StateStopping}
	allEvents := []Event{EventStart, EventStreamStarted, EventStop, EventFatalError, EventWorkerIdle}

	for _, from := range allStates {
		for _, ev := range allEvents {
			if valid[from][ev] {
				continue
			}
			got, err := Next(from, ev)
			var ite InvalidTransitionError
			if !errors.As(err, &ite) {
				t.Errorf("Next(%s, %s) error = %v, want InvalidTransitionError", from, ev, err)
			}
			if got != from {
				t.Errorf("Next(%s, %s) returned state %s, want unchanged %s", from, ev, got, from)
			}
		}
	}
}

func TestFatalErrorFromWatchingRoutesToStopping(t *testing.T) {
	got, err := Next(StateWatching, EventFatalError)
	if err != nil || got != StateStopping {
		t.Fatalf("Next(WATCHING, FATAL_ERROR) = %s, %v; want STOPPING, nil", got, err)
	}
}

func TestHappyPathRoundTrip(t *testing.T) {
	state := StateOff
	for _, ev := range []Event{EventStart, EventStreamStarted, EventStop, EventWorkerIdle} {
		next, err := Next(state, ev)
		if err != nil {
			t.Fatalf("Next(%s, %s): %v", state, ev, err)
		}
		state = next
	}
	if state != StateOff {
		t.Fatalf("round trip ended in %s, want OFF", state)
	}
}
