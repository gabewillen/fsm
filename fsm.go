package fsm

type Event string

type Action func(*FSM, Event, interface{})
type Constraint func(*FSM, Event, interface{}) bool

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
	onDispatch   func(*FSM, Event, interface{})
	onTransition func(*FSM, Event, string, string)
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

	return fsm
}

func Initial(id string) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		fsm.initial = id
		fsm.current = id
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
			transition.target = target
		case PartialNode:
			target(fsm, state, transition)
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
func (f *FSM) OnTransition(fn func(*FSM, Event, string, string)) {
	f.onTransition = fn
}

// Exit sets a func that will be called when exiting any state.
func (f *FSM) OnDispatch(fn func(*FSM, Event, interface{})) {
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
		if transition.guard != nil && !transition.guard(fsm, event, data) {
			continue
		}
		target, ok := fsm.states[transition.target]
		if !ok {

			return true
		}
		if ok && state.exit != nil {
			state.exit(fsm, event, data)
		}
		if transition.effect != nil {
			transition.effect(fsm, event, data)
		}
		if fsm.onTransition != nil {
			fsm.onTransition(fsm, event, fsm.current, transition.target)
		}
		fsm.current = transition.target
		if ok && target.enter != nil {
			target.enter(fsm, event, data)
		}
		return true
	}
	return false
}
