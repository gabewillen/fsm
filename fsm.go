package fsm

import (
	"context"
	"sync"
)

// required for type guard
type Node interface{}

// type Event interface {
// 	Node
// 	Kind() string
// }

// type EventNode struct {
// 	kind string
// }

//	func (node *EventNode) Kind() string {
//		return node.kind
//	}
type Event string
type Action func(context.Context, Event, any)
type Constraint func(Event, any) bool

type BehaviorNode struct {
	invoke    Action
	ctx       context.Context
	execution sync.WaitGroup
	cancel    context.CancelFunc
}

func (node *BehaviorNode) execute(ctx context.Context, event Event, data any) *BehaviorNode {
	if node == nil || node.invoke == nil {
		return nil
	}
	node.execution.Add(1)
	node.ctx, node.cancel = context.WithCancel(ctx)
	go func() {
		node.invoke(node.ctx, event, data)
		node.execution.Done()
	}()
	return node
}

func (node *BehaviorNode) wait() {
	if node == nil {
		return
	}
	node.execution.Wait()
}

func (node *BehaviorNode) terminate() {
	if node == nil || node.cancel == nil {
		return
	}
	node.cancel()
	node.execution.Wait()
}

type StateNode struct {
	entry       *BehaviorNode
	activity    *BehaviorNode
	exit        *BehaviorNode
	transitions []*TransitionNode
	submachine  *FSM
}

func (state *StateNode) enter(ctx context.Context, event Event, data any) {
	if state == nil {
		return
	}
	// execute entry action
	state.entry.execute(ctx, event, data)
	// wait for entry action to complete
	state.entry.wait()
	// execute activity action this runs in a goroutine
	state.activity.execute(ctx, event, data)
	if state.submachine != nil {
		state.submachine.execute(ctx, event, data)
	}
}

func (state *StateNode) leave(ctx context.Context, event Event, data any) {
	if state == nil {
		return
	}
	state.activity.terminate()
	state.exit.execute(ctx, event, data)
	state.exit.wait()
}

type TransitionNode struct {
	events []Event
	guard  Constraint
	effect *BehaviorNode
	target string
}

// FSM is a finite state machine.
type FSM struct {
	*StateNode
	*BehaviorNode
	states       map[string]*StateNode
	onDispatch   func(Event, any)
	onTransition func(Event, string, string)
	current      string
	initial      string
	mutex        *sync.Mutex
}

type PartialNode func(*FSM, *StateNode, *TransitionNode)

// New creates a new finite state machine having the specified initial state.
func New(nodes ...PartialNode) *FSM {
	fsm := Model(nodes...)
	initial, ok := fsm.states[fsm.initial]
	if !ok {
		return fsm
	}
	initial.enter(fsm.ctx, "", nil)
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
		state.entry = &BehaviorNode{
			invoke: fn,
		}
	}
}

func Activity(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.activity = &BehaviorNode{
			invoke: fn,
		}
	}
}

func Exit(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, _ *TransitionNode) {
		state.exit = &BehaviorNode{
			invoke: fn,
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

type Dispatchable interface {
	string | Node | PartialNode
}

// On defines the Event that triggers a Transition.
func On[E Dispatchable](events ...E) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				transition.events = append(transition.events, Event(any(evt).(string)))
			// case Event:
			// 	transition.events = append(transition.events, &EventNode{kind: any(evt).(Event).Kind()})
			case PartialNode:
				any(evt).(PartialNode)(fsm, state, transition)
			}
		}
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

func Submachine(id string, submachine *FSM) PartialNode {
	partialState := State(id)
	return func(fsm *FSM, _ *StateNode, _ *TransitionNode) {
		partialState(fsm, nil, nil)
		state := fsm.states[id]
		state.submachine = &FSM{
			BehaviorNode: &BehaviorNode{},
			states:       fsm.states,
			mutex:        fsm.mutex,
		}
	}
}

// // Call defines a function that is called when a Transition occurs.
func Effect(fn Action) PartialNode {
	return func(fsm *FSM, state *StateNode, transition *TransitionNode) {
		if transition == nil {
			return
		}
		transition.effect = &BehaviorNode{
			invoke: fn,
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
	if fsm == nil {
		return false
	}
	if fsm.submachine != nil {
		return fsm.submachine.Dispatch(event, data)
	}
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
			state.leave(fsm.ctx, event, data)
		}
		if transition.effect != nil {
			transition.effect.execute(fsm.ctx, event, data)
			transition.effect.wait()
		}
		if fsm.onTransition != nil {
			fsm.onTransition(event, fsm.current, transition.target)
		}
		fsm.current = transition.target
		if ok {
			target.enter(fsm.ctx, event, data)
		}
		return true
	}
	return false
}

func Model(nodes ...PartialNode) *FSM {
	fsm := &FSM{
		StateNode: &StateNode{
			transitions: []*TransitionNode{},
		},
		BehaviorNode: &BehaviorNode{
			ctx: context.Background(),
		},
		states: map[string]*StateNode{},
		mutex:  &sync.Mutex{},
	}
	for _, partial := range nodes {
		partial(fsm, nil, nil)
	}
	return fsm
}
