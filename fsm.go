package fsm

import (
	"context"
	"log/slog"
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
type Constraint func(context.Context, Event, any) bool

type behavior struct {
	action    Action
	execution sync.WaitGroup
	cancel    context.CancelFunc
	mutex     *sync.Mutex
}

func (element *behavior) execute(ctx context.Context, event Event, data any) *behavior {
	if element == nil || element.action == nil {
		return nil
	}
	element.execution.Add(1)
	ctx, element.cancel = context.WithCancel(ctx)
	go func() {
		element.action(ctx, event, data)
		element.execution.Done()
	}()
	return element
}

func (node *behavior) wait() {
	if node == nil {
		return
	}
	node.execution.Wait()
}

func (node *behavior) terminate() {
	if node == nil || node.cancel == nil {
		return
	}
	node.cancel()
	node.execution.Wait()
}

type state struct {
	name        string
	entry       *behavior
	activity    *behavior
	exit        *behavior
	transitions []*transition
	submachine  *FSM
}

func (statePtr *state) enter(ctx context.Context, event Event, data any) {
	if statePtr == nil {
		return
	}
	// execute entry action
	statePtr.entry.execute(ctx, event, data)
	// wait for entry action to complete
	statePtr.entry.wait()
	// execute activity action this runs in a goroutine
	statePtr.activity.execute(ctx, event, data)
	if statePtr.submachine != nil {
		statePtr.submachine.execute(ctx, event, data)
	}
}

func (statePtr *state) leave(ctx context.Context, event Event, data any) {
	if statePtr == nil {
		return
	}
	if statePtr.submachine != nil {
		statePtr.submachine.terminate()
		statePtr.submachine.Reset()
	}
	statePtr.activity.terminate()
	statePtr.exit.execute(ctx, event, data)
	statePtr.exit.wait()

}

func (element *state) Name() string {
	if element == nil {
		return ""
	}
	return element.name
}

func (element *state) Submachine() *FSM {
	if element == nil {
		return nil
	}
	return element.submachine
}

type transition struct {
	events []Event
	guard  Constraint
	effect *behavior
	target string
}

type model struct {
	*behavior
	states          map[string]*state
	current         string
	initial         string
	submachineState *state
}

// type model struct {
// 	*statemachine
// }

// FSM is a finite state machine.
type FSM struct {
	*model
	onDispatch   func(Event, any)
	onTransition func(Event, string, string)
	ctx          context.Context
}

type PartialElement func(*model, *state, *transition)

// New creates a new finite state machine having the specified initial state.
func New(context context.Context, stateMachineModel *model) *FSM {
	fsm := &FSM{
		model: stateMachineModel,
		ctx:   context,
	}
	slog.Info("New FSM", "fsm", fsm)
	fsm.behavior.execute(fsm.ctx, "", nil)
	fsm.behavior.wait()
	return fsm
}

func Initial(id string, partialElements ...PartialElement) PartialElement {
	return func(model *model, _ *state, _ *transition) {
		this, ok := model.states[id]
		if !ok {
			this = &state{
				name: id,
			}
			model.states[id] = this
		}
		model.initial = id
		model.current = id
		for _, partial := range partialElements {
			partial(model, this, nil)
		}
	}
}

func State(id string, partialElements ...PartialElement) PartialElement {
	return func(model *model, _ *state, _ *transition) {
		this := &state{
			name: id,
		}
		for _, partial := range partialElements {
			partial(model, this, nil)
		}
		model.states[id] = this
	}
}

func Entry(fn Action) PartialElement {
	return func(model *model, state *state, _ *transition) {
		state.entry = &behavior{
			action: fn,
		}
	}
}

func Activity(fn Action) PartialElement {
	return func(model *model, state *state, _ *transition) {
		state.activity = &behavior{
			action: fn,
		}
	}
}

func Exit(fn Action) PartialElement {
	return func(model *model, state *state, _ *transition) {
		state.exit = &behavior{
			action: fn,
		}
	}
}

// Src defines the source States for a Transition.
func Source(sources ...string) PartialElement {
	return func(model *model, _ *state, transition *transition) {
		if transition == nil {
			return
		}
		for _, src := range sources {
			source, ok := model.states[src]
			if !ok {
				source = &state{}
				model.states[src] = source
			}
			source.transitions = append(source.transitions, transition)
		}
	}
}

type Dispatchable interface {
	string | Node | PartialElement
}

// On defines the Event that triggers a Transition.
func On[E Dispatchable](events ...E) PartialElement {
	return func(model *model, _ *state, transition *transition) {
		if transition == nil {
			return
		}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				transition.events = append(transition.events, Event(any(evt).(string)))
			// case Event:
			// 	transition.events = append(transition.events, &EventNode{kind: any(evt).(Event).Kind()})
			case PartialElement:
				any(evt).(PartialElement)(model, nil, transition)
			}
		}
	}
}

type Targetable interface {
	string | PartialElement
}

// Dst defines the new State the machine switches to after a Transition.
func Target[T Targetable](target T) PartialElement {
	return func(model *model, _ *state, transitionElement *transition) {
		if transitionElement == nil {
			return
		}
		switch target := any(target).(type) {
		case string:
			if _, ok := model.states[target]; !ok {
				model.states[target] = &state{
					transitions: []*transition{},
				}
			}
			transitionElement.target = target
		case PartialElement:
			target(model, nil, transitionElement)
		}
	}
}

func Choice(transitions ...PartialElement) PartialElement {
	return func(model *model, _ *state, transitionElement *transition) {
		for _, transition := range transitions {
			transition(model, nil, transitionElement)
		}
	}
}

// Check is an external condition that allows a Transition only if fn returns true.
func Guard(fn Constraint) PartialElement {
	return func(model *model, _ *state, transitionElement *transition) {
		if transitionElement == nil {
			return
		}
		transitionElement.guard = fn
	}
}

func Submachine(submachine *model) PartialElement {
	return func(modelPtr *model, statePtr *state, _ *transition) {
		if statePtr == nil {
			slog.Warn("Submachine called on nil state")
			return
		}
		statePtr.submachine = &FSM{
			model: &model{
				behavior: &behavior{
					mutex: modelPtr.behavior.mutex,
				},
				states:          submachine.states,
				current:         submachine.initial,
				initial:         submachine.initial,
				submachineState: statePtr,
			},
		}
	}
}

// // Call defines a function that is called when a Transition occurs.
func Effect(fn Action) PartialElement {
	return func(model *model, _ *state, transitionElement *transition) {
		if transitionElement == nil {
			return
		}
		transitionElement.effect = &behavior{
			action: fn,
		}
	}
}

func Transition(nodes ...PartialElement) PartialElement {
	return func(model *model, stateElement *state, _ *transition) {
		transition := &transition{}
		for _, node := range nodes {
			node(model, nil, transition)
		}
		if stateElement != nil {
			stateElement.transitions = append(stateElement.transitions, transition)
		}
	}
}

// Reset resets the machine to its initial state.
func (f *FSM) Reset() {
	f.current = f.initial
}

// Current returns the current state.
func (f *FSM) State() *state {
	return f.states[f.current]
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
	// if fsm.submachine != nil {
	// 	return fsm.submachine.Dispatch(event, data)
	// }
	state, ok := fsm.states[fsm.current]
	if !ok {
		return false
	}

	if state.submachine != nil && fsm.submachineState != state && state.submachine.Dispatch(event, data) {
		return true
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
		if transition.guard != nil && !transition.guard(fsm.ctx, event, data) {
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

func Model(elements ...PartialElement) *model {
	newModel := &model{
		behavior: &behavior{
			mutex: &sync.Mutex{},
		},
		states: map[string]*state{},
	}
	for _, partial := range elements {
		partial(newModel, nil, nil)
	}
	newModel.behavior.action = func(ctx context.Context, event Event, data any) {
		slog.Info("Model behavior execution", "event", event, "data", data, "model", newModel)
		initial, ok := newModel.states[newModel.initial]
		slog.Info("WE ARE RIGHT HERE behavior execution", "initial", initial, "ok", ok)

		if !ok {
			return
		}
		initial.enter(ctx, "", nil)
	}
	return newModel
}
