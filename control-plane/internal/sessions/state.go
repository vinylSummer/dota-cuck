// Package sessions holds the control-plane session lifecycle.
//
// The state machine here is pure (no I/O, no DB) so it can be unit-tested in
// isolation and reasoned about independently of the HTTP/gRPC plumbing that
// drives it.
package sessions

import "fmt"

// State is a control-plane session state. Values match the Session State
// Machine in CLAUDE.md.
type State string

const (
	StateOff      State = "OFF"
	StateStarting State = "STARTING"
	StateWatching State = "WATCHING"
	StateStopping State = "STOPPING"
)

// Event drives a session transition.
type Event string

const (
	EventStart         Event = "START"          // POST /api/sessions
	EventStreamStarted Event = "STREAM_STARTED" // StreamStarted event from worker
	EventStop          Event = "STOP"           // DELETE /api/sessions/:id
	EventFatalError    Event = "FATAL_ERROR"    // fatal ErrorEvent from worker
	EventWorkerIdle    Event = "WORKER_IDLE"    // StatusUpdate IDLE from worker
)

// InvalidTransitionError is returned by Next for an undefined edge.
type InvalidTransitionError struct {
	From  State
	Event Event
}

func (e InvalidTransitionError) Error() string {
	return fmt.Sprintf("invalid transition: %s in state %s", e.Event, e.From)
}

// transitions is the full edge set:
//
//	OFF      --start--------------------> STARTING
//	STARTING --stream_started-----------> WATCHING
//	STARTING --stop|fatal_error---------> STOPPING
//	WATCHING --stop|fatal_error---------> STOPPING
//	STOPPING --worker_idle--------------> OFF
var transitions = map[State]map[Event]State{
	StateOff:      {EventStart: StateStarting},
	StateStarting: {EventStreamStarted: StateWatching, EventStop: StateStopping, EventFatalError: StateStopping},
	StateWatching: {EventStop: StateStopping, EventFatalError: StateStopping},
	StateStopping: {EventWorkerIdle: StateOff},
}

// Next returns the state reached by applying ev to cur, or an
// InvalidTransitionError if the edge is undefined.
func Next(cur State, ev Event) (State, error) {
	if next, ok := transitions[cur][ev]; ok {
		return next, nil
	}
	return cur, InvalidTransitionError{From: cur, Event: ev}
}
