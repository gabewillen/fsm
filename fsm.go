package fsm

type Event string

type Interface interface{}

type Action[T Interface] func(T, Event, interface{})
type Constraint[T Interface] func(T, Event, interface{}) bool

type StateNode[T Interface] struct {
	enter       Action[T]
	activity    Action[T]
	exit        Action[T]
	transitions []*TransitionNode[T]
}

type TransitionNode[T Interface] struct {
	events []Event
	guard  Constraint[T]
	effect Action[T]
	target string
}

// FSM is a finite state machine.
type FSM[T Interface] struct {
	Interface
	states       map[string]*StateNode[T]
	onDispatch   func(T, Event, interface{})
	onTransition func(T, Event, string, string)
	current      string
	initial      string
}

type PartialNode[T Interface] func(*FSM[T], *StateNode[T], *TransitionNode[T])

// New creates a new finite state machine having the specified initial state.
func New[T Interface](nodes ...PartialNode[T]) *FSM[T] {
	fsm := &FSM[T]{
		states: map[string]*StateNode[T]{},
	}
	for _, partial := range nodes {
		partial(fsm, nil, nil)
	}

	return fsm
}

func Initial(id string, nodes ...PartialNode[Interface]) PartialNode[Interface] {
	return func(fsm *FSM[Interface], state *StateNode[Interface], transition *TransitionNode[Interface]) {
		if _, ok := fsm.states[id]; !ok {
			fsm.states[id] = &StateNode[Interface]{}
		}
		fsm.initial = id
		fsm.current = id
		for _, node := range nodes {
			node(fsm, nil, nil)
		}
	}
}

func State(id string, nodes ...PartialNode[Interface]) PartialNode[Interface] {
	return func(fsm *FSM[Interface], _ *StateNode[Interface], _ *TransitionNode[Interface]) {
		state := &StateNode[Interface]{}
		for _, node := range nodes {
			node(fsm, state, nil)
		}
		fsm.states[id] = state
	}
}

func Entry[T Interface](fn Action[T]) PartialNode[T] {
	return func(fsm *FSM[T], state *StateNode[T], _ *TransitionNode[T]) {
		state.enter = fn
	}
}

func Activity[T Interface](fn Action[T]) PartialNode[T] {
	return func(fsm *FSM[T], state *StateNode[T], _ *TransitionNode[T]) {
		state.activity = fn
	}
}

func Exit[T Interface](fn Action[T]) PartialNode[T] {
	return func(fsm *FSM[T], state *StateNode[T], _ *TransitionNode[T]) {
		state.exit = fn
	}
}

// Src defines the source States for a Transition.
func Source(source ...string) PartialNode[Interface] {
	return func(fsm *FSM[Interface], state *StateNode[Interface], transition *TransitionNode[Interface]) {
		if transition == nil {
			return
		}
		for _, src := range source {
			state, ok := fsm.states[src]
			if !ok {
				state = &StateNode[Interface]{}
				fsm.states[src] = state
			}
			state.transitions = append(state.transitions, transition)
		}
	}
}

// On defines the Event that triggers a Transition.
func On(events ...Event) PartialNode[Interface] {
	return func(fsm *FSM[Interface], state *StateNode[Interface], transition *TransitionNode[Interface]) {
		if transition == nil {
			return
		}
		transition.events = append(transition.events, events...)
	}
}

type Targetable[T Interface] interface {
	string | PartialNode[T]
}

// Dst defines the new State the machine switches to after a Transition.
func Target[T Targetable[Interface]](target T) PartialNode[Interface] {
	return func(fsm *FSM[Interface], state *StateNode[Interface], transition *TransitionNode[Interface]) {
		if transition == nil {
			return
		}
		switch target := any(target).(type) {
		case string:
			if _, ok := fsm.states[target]; !ok {
				fsm.states[target] = &StateNode[Interface]{}
			}
			transition.target = target
		case PartialNode[Interface]:
			target(fsm, state, transition)
		}
	}
}

func Choice(transitions ...PartialNode[Interface]) PartialNode[Interface] {
	return func(fsm *FSM[Interface], state *StateNode[Interface], _ *TransitionNode[Interface]) {
		for _, transition := range transitions {
			transition(fsm, state, nil)
		}
	}
}

// Check is an external condition that allows a Transition only if fn returns true.
func Guard[T Interface](fn Constraint[T]) PartialNode[T] {
	return func(fsm *FSM[T], state *StateNode[T], transition *TransitionNode[T]) {
		if transition == nil {
			return
		}
		transition.guard = fn
	}
}

// // Call defines a function that is called when a Transition occurs.
func Effect[T Interface](fn Action[T]) PartialNode[T] {
	return func(fsm *FSM[T], state *StateNode[T], transition *TransitionNode[T]) {
		if transition == nil {
			return
		}
		transition.effect = fn
	}
}

func Transition(nodes ...PartialNode[Interface]) PartialNode[Interface] {
	return func(fsm *FSM[Interface], state *StateNode[Interface], _ *TransitionNode[Interface]) {
		transition := &TransitionNode[Interface]{}
		for _, node := range nodes {
			node(fsm, nil, transition)
		}
		if state != nil {
			state.transitions = append(state.transitions, transition)
		}
	}
}

// Reset resets the machine to its initial state.
func (f *FSM[T]) Reset() {
	f.current = f.initial
}

// Current returns the current state.
func (f *FSM[T]) State() string {
	return f.current
}

// Enter sets a func that will be called when entering any state.
func (f *FSM[T]) OnTransition(fn func(T, Event, string, string)) {
	f.onTransition = fn
}

// Exit sets a func that will be called when exiting any state.
func (f *FSM[T]) OnDispatch(fn func(T, Event, interface{})) {
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
func (fsm *FSM[T]) Dispatch(event Event, data interface{}) bool {
	state, ok := fsm.states[fsm.current]
	if !ok {
		return false
	}
	this, ok := any(fsm).(T)
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
		if transition.guard != nil && !transition.guard(this, event, data) {
			continue
		}
		target, ok := fsm.states[transition.target]
		if !ok {
			return true
		}
		if ok && state.exit != nil {
			state.exit(this, event, data)
		}
		if transition.effect != nil {
			transition.effect(this, event, data)
		}
		if fsm.onTransition != nil {
			fsm.onTransition(this, event, fsm.current, transition.target)
		}
		fsm.current = transition.target
		if ok && target.enter != nil {
			target.enter(this, event, data)
		}
		return true
	}
	return false
}
