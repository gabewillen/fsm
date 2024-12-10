package fsm

import (
	"fmt"
)

type Event string

type Action func(Event, interface{})
type Constraint func(Event, interface{}) bool

type StateNode struct {
	enter       Action
	activity    Action
	exit        Action
	transitions []*TransitionNode
}

type TransitionNode struct {
	events []Event
	guard  Constraint
	effect Action
	target string
}

// FSM is a finite state machine.
type FSM struct {
	states       map[string]*StateNode
	onDispatch   func(Event, interface{})
	onTransition func(Event, string, string)
	current      string
	initial      string
}

type PartialNode func(*FSM, *StateNode, *TransitionNode)

// New creates a new finite state machine having the specified initial state.
func New(nodes ...PartialNode) *FSM {
	fsm := &FSM{
		states: map[string]*StateNode{},
	}
	for _, partial := range nodes {
		partial(fsm, nil, nil)
	}
	initial, ok := fsm.states[fsm.initial]
	if !ok {
		panic(fmt.Sprintf("initial state %s not found", fsm.initial))
	}
	if initial.enter != nil {
		initial.enter("", nil)
	}
	return fsm
}

func Initial(id string, nodes ...PartialNode) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if _, ok := fsm.states[id]; !ok {
			fsm.states[id] = &StateNode{}
		}
		fsm.initial = id
		fsm.current = id
		for _, node := range nodes {
			node(fsm, nil, nil)
		}
	}
}

func State(id string, nodes ...PartialNode) PartialNode {
	return func(fsm *FSM, _ *StateNode, _ *TransitionNode) {
		state := &StateNode{}
		for _, node := range nodes {
			node(fsm, state, nil)
		}
		fsm.states[id] = state
	}
}

func Entry(fn func(event Event, data interface{})) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.enter = fn
	}
}

func Activity(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.activity = fn
	}
}

func Exit(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.exit = fn
	}
}

// Src defines the source States for a Transition.
func Source(source ...string) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		for _, src := range source {
			state, ok := fsm.states[src]
			if !ok {
				state = &StateNode{}
				fsm.states[src] = state
			}
			state.transitions = append(state.transitions, transition)
		}
	}
}

// On defines the Event that triggers a Transition.
func On(events ...Event) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		transition.events = append(transition.events, events...)
	}
}

type Targetable interface {
	string | PartialNode
}

// Dst defines the new State the machine switches to after a Transition.
func Target[T Targetable](target T) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		switch target := any(target).(type) {
		case string:
			if _, ok := fsm.states[target]; !ok {
				fsm.states[target] = &StateNode{}
			}
			transition.target = target
		case PartialNode:
			target(fsm, state, transition)
		}
	}
}

func Choice(transitions ...PartialNode) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		for _, transition := range transitions {
			transition(fsm, state, nil)
		}
	}
}

// Check is an external condition that allows a Transition only if fn returns true.
func Guard(fn Constraint) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		transition.guard = fn
	}
}

// // Call defines a function that is called when a Transition occurs.
func Effect(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		transition.effect = fn
	}
}

func Transition(nodes ...PartialNode) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		transition := &TransitionNode{}
		for _, node := range nodes {
			node(fsm, nil, transition)
		}
		if state != nil {
			state.transitions = append(state.transitions, transition)
		}
	}
}

// Reset resets the machine to its initial state.
func (f *FSM) Reset() {
	f.current = f.initial
}

// Current returns the current state.
func (f *FSM) State() string {
	return f.current
}

// Enter sets a func that will be called when entering any state.
func (f *FSM) OnTransition(fn func(Event, string, string)) {
	f.onTransition = fn
}

// Exit sets a func that will be called when exiting any state.
func (f *FSM) OnDispatch(fn func(Event, interface{})) {
	f.onDispatch = fn
}

func find[T any](slice []T, fn func(T) bool) (T, bool) {
	var res T
	for _, item := range slice {
		if fn(item) {
			return item, true
		}
	}
	return res, false
}

// Event send an Event to a machine, applying at most one transition.
// true is returned if a transition has been applied, false otherwise.
func (fsm *FSM) Dispatch(event Event, data interface{}) bool {
	state, ok := fsm.states[fsm.current]
	if !ok {
		return false
	}
	for _, transition := range state.transitions {
		_, ok := find(transition.events, func(evt Event) bool {
			return evt == event
		})
		if !ok {
			continue
		}
		if transition.guard != nil && !transition.guard(event, data) {
			continue
		}
		target, ok := fsm.states[transition.target]
		if !ok {
			return true
		}
		if ok && state.exit != nil {
			state.exit(event, data)
		}
		if transition.effect != nil {
			transition.effect(event, data)
		}
		if fsm.onTransition != nil {
			fsm.onTransition(event, fsm.current, transition.target)
		}
		fsm.current = transition.target
		if ok && target.enter != nil {
			target.enter(event, data)
		}
		return true
	}
	return false
}
