package fsm

import (
	"sync"
)

type Event string

type Action func(chan bool, Event, any)
type Constraint func(Event, any) bool

type ActionNode struct {
	execute   Action
	cancel    chan bool
	execution sync.WaitGroup
}

func (node *ActionNode) Execute(event Event, data any) *ActionNode {
	if node == nil || node.execute == nil {
		return nil
	}
	node.execution.Add(1)
	go func() {
		node.execute(node.cancel, event, data)
		node.execution.Done()
	}()
	return node
}

func (node *ActionNode) Wait() {
	if node == nil {
		return
	}
	node.execution.Wait()
}

func (node *ActionNode) Terminate() {
	if node == nil || node.cancel == nil {
		return
	}
	node.cancel <- true
	node.execution.Wait()
}

type StateNode struct {
	entry       *ActionNode
	activity    *ActionNode
	exit        *ActionNode
	transitions []*TransitionNode
}

func (state *StateNode) enter(event Event, data any) {
	if state == nil {
		return
	}
	// execute entry action
	state.entry.Execute(event, data)
	// wait for entry action to complete
	state.entry.Wait()
	// execute activity action this runs in a goroutine
	state.activity.Execute(event, data)
}

func (state *StateNode) leave(event Event, data any) {
	if state == nil {
		return
	}
	state.activity.Terminate()
	state.exit.Execute(event, data)
	state.exit.Wait()
}

type TransitionNode struct {
	events []Event
	guard  Constraint
	effect *ActionNode
	target string
}

// FSM is a finite state machine.
type FSM struct {
	*StateNode
	states       map[string]*StateNode
	onDispatch   func(Event, any)
	onTransition func(Event, string, string)
	current      string
	initial      string
	mutex        sync.Mutex
}

type PartialNode func(*FSM, *StateNode, *TransitionNode)

// New creates a new finite state machine having the specified initial state.
func New(nodes ...PartialNode) *FSM {
	fsm := &FSM{
		states: map[string]*StateNode{},
		mutex:  sync.Mutex{},
	}
	for _, partial := range nodes {
		partial(fsm, nil, nil)
	}
	initial, ok := fsm.states[fsm.initial]
	if !ok {
		return fsm
	}
	initial.enter("", nil)
	return fsm
}

func Initial(id string, nodes ...PartialNode) PartialNode {
	return func(fsm *FSM, _ *StateNode, _ *TransitionNode) {
		state, ok := fsm.states[id]
		if !ok {
			state = &StateNode{}
			fsm.states[id] = state
		}
		fsm.initial = id
		fsm.current = id
		for _, node := range nodes {
			node(fsm, state, nil)
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

func Entry(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.entry = &ActionNode{
			execute: fn,
			cancel:  make(chan bool, 1),
		}
	}
}

func Activity(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.activity = &ActionNode{
			execute: fn,
			cancel:  make(chan bool, 1),
		}
	}
}

func Exit(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.exit = &ActionNode{
			execute: fn,
			cancel:  make(chan bool, 1),
		}
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
		transition.effect = &ActionNode{
			execute: fn,
			cancel:  make(chan bool, 1),
		}
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
func (f *FSM) OnDispatch(fn func(Event, any)) {
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
func (fsm *FSM) Dispatch(event Event, data any) bool {
	state, ok := fsm.states[fsm.current]
	if !ok {
		return false
	}
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()
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
		if ok {
			state.leave(event, data)
		}
		if transition.effect != nil {
			transition.effect.Execute(event, data)
			transition.effect.Wait()
		}
		if fsm.onTransition != nil {
			fsm.onTransition(event, fsm.current, transition.target)
		}
		fsm.current = transition.target
		if ok {
			target.enter(event, data)
		}
		return true
	}
	return false
}
