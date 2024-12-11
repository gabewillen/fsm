package fsm

import (
	"context"
	"log/slog"
	"path"
	"sync"
)

// required for type guard
type node interface{}

type Context struct {
	*FSM
	context.Context
}

//	type Event interface {
//		Node
//		Kind() string
//	}

// type EventNode struct {
// 	kind string
// }

//	func (node *EventNode) Kind() string {
//		return node.kind
//	}
type Event string
type Action func(Context, Event, any)
type Constraint func(Context, Event, any) bool

type constraint struct {
	expr Constraint
}

func (element *constraint) evaluate(fsm *FSM, event Event, data any) bool {
	if element == nil {
		return true
	}
	return element.expr(Context{FSM: fsm, Context: fsm.ctx}, event, data)
}

type behavior struct {
	action    Action
	execution sync.WaitGroup
	cancel    context.CancelFunc
	mutex     *sync.Mutex
}

func (element *behavior) execute(fsm *FSM, event Event, data any) *behavior {
	if element == nil || element.action == nil {
		return nil
	}
	element.execution.Add(1)
	var ctx context.Context
	ctx, element.cancel = context.WithCancel(fsm.ctx)
	go func() {
		element.action(Context{FSM: fsm, Context: ctx}, event, data)
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

func (statePtr *state) enter(fsm *FSM, event Event, data any) {
	if statePtr == nil {
		return
	}
	// execute entry action
	statePtr.entry.execute(fsm, event, data)
	// wait for entry action to complete
	statePtr.entry.wait()
	// execute activity action this runs in a goroutine
	statePtr.activity.execute(fsm, event, data)
	if statePtr.submachine != nil {
		statePtr.submachine.ctx = fsm.ctx
		statePtr.submachine.execute(statePtr.submachine, event, data)
		statePtr.submachine.wait()
	}
}

func (statePtr *state) leave(fsm *FSM, event Event, data any) {
	if statePtr == nil {
		return
	}
	if statePtr.submachine != nil {
		statePtr.submachine.terminate()
		statePtr.submachine.Reset()
	}
	statePtr.activity.terminate()
	statePtr.exit.execute(fsm, event, data)
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
	guard  *constraint
	effect *behavior
	target string
	source string
}

type Modeled struct {
	*behavior
	states          map[string]*state
	current         string
	submachineState *state
}

// type model struct {
// 	*statemachine
// }

type Trace struct {
	Kind         string
	Event        string
	CurrentState string
	TargetState  string
	Data         any
}

// FSM is a finite state machine.
type FSM struct {
	*Modeled
	ctx       context.Context
	listeners map[int]func(Trace)
}

type PartialElement func(*Modeled, *state, *transition)

// New creates a new finite state machine having the specified initial state.
func New(context context.Context, stateMachineModel *Modeled) *FSM {
	fsm := &FSM{
		Modeled: &Modeled{
			behavior: &behavior{
				action:    stateMachineModel.behavior.action,
				execution: sync.WaitGroup{},
				mutex:     &sync.Mutex{},
			},
			states:  stateMachineModel.states,
			current: stateMachineModel.current,
		},
		ctx:       context,
		listeners: map[int]func(Trace){},
	}
	fsm.Modeled.behavior.execute(fsm, "", nil)
	// go fsm.Dispatch("", nil)
	return fsm
}

func Initial(id string, partialElements ...PartialElement) PartialElement {
	return func(model *Modeled, _ *state, _ *transition) {
		initial, ok := model.states[""]
		if !ok {
			slog.Warn("No initial state found")
			return
		}
		target, ok := model.states[id]
		if !ok {
			target = &state{
				name:        id,
				transitions: []*transition{},
			}
			model.states[id] = target
		}
		transition := &transition{
			events: []Event{},
			target: id,
		}
		for _, partial := range partialElements {
			partial(model, target, transition)
		}
		initial.transitions = append(initial.transitions, transition)
	}
}

func State(id string, partialElements ...PartialElement) PartialElement {
	return func(model *Modeled, _ *state, _ *transition) {
		this, ok := model.states[id]
		if !ok {
			this = &state{
				name:        id,
				transitions: []*transition{},
			}
			model.states[id] = this
		}
		for _, partial := range partialElements {
			partial(model, this, nil)
		}
	}
}

func Entry(fn Action) PartialElement {
	return func(model *Modeled, state *state, _ *transition) {
		state.entry = &behavior{
			action: fn,
		}
	}
}

func Activity(fn Action) PartialElement {
	return func(model *Modeled, state *state, _ *transition) {
		state.activity = &behavior{
			action: fn,
		}
	}
}

func Exit(fn Action) PartialElement {
	return func(model *Modeled, state *state, _ *transition) {
		state.exit = &behavior{
			action: fn,
		}
	}
}

// Src defines the source States for a Transition.
func Source(sources ...string) PartialElement {
	return func(model *Modeled, _ *state, transitionPtr *transition) {
		if transitionPtr == nil {
			return
		}
		for _, src := range sources {
			source, ok := model.states[src]
			if !ok {
				source = &state{
					name:        src,
					transitions: []*transition{},
				}
				model.states[src] = source
			}
			source.transitions = append(source.transitions, transitionPtr)
		}
	}
}

type Dispatchable interface {
	string | node | PartialElement
}

// On defines the Event that triggers a Transition.
func On[E Dispatchable](events ...E) PartialElement {
	return func(model *Modeled, _ *state, transition *transition) {
		if transition == nil {
			return
		}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				transition.events = append(transition.events, Event(any(evt).(string)))
			case Event:
				transition.events = append(transition.events, any(evt).(Event))
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
	return func(model *Modeled, _ *state, transitionElement *transition) {
		if transitionElement == nil {
			return
		}
		switch target := any(target).(type) {
		case string:
			if _, ok := model.states[target]; !ok {
				model.states[target] = &state{
					name:        target,
					transitions: []*transition{},
				}
			}
			transitionElement.target = target
		case PartialElement:
			target(model, nil, transitionElement)
		}
	}
}

// func Choice(transitions ...PartialElement) PartialElement {
// 	return func(model *Modeled, _ *state, transitionElement *transition) {
// 		for _, transition := range transitions {
// 			transition(model, nil, transitionElement)
// 		}
// 	}
// }

// Check is an external condition that allows a Transition only if fn returns true.
func Guard(fn Constraint) PartialElement {
	return func(model *Modeled, _ *state, transitionElement *transition) {
		if transitionElement == nil {
			return
		}
		transitionElement.guard = &constraint{
			expr: fn,
		}
	}
}

func Submachine(submachine *Modeled) PartialElement {
	return func(modelPtr *Modeled, statePtr *state, _ *transition) {
		if statePtr == nil {
			slog.Warn("Submachine called on nil state")
			return
		}
		statePtr.submachine = &FSM{
			Modeled: &Modeled{
				behavior: &behavior{
					action:    submachine.behavior.action,
					execution: sync.WaitGroup{},
					mutex:     modelPtr.behavior.mutex,
				},
				states:          submachine.states,
				submachineState: statePtr,
			},
			listeners: map[int]func(Trace){},
			ctx:       context.Background(),
		}
	}
}

func Effect(fn Action) PartialElement {
	return func(model *Modeled, _ *state, transitionElement *transition) {
		if transitionElement == nil {
			return
		}
		transitionElement.effect = &behavior{
			action: fn,
		}
	}
}

func Transition(nodes ...PartialElement) PartialElement {
	return func(model *Modeled, stateElement *state, _ *transition) {
		transition := &transition{}
		for _, node := range nodes {
			node(model, nil, transition)
		}
		if stateElement != nil {
			transition.source = stateElement.name
			stateElement.transitions = append(stateElement.transitions, transition)
		}
	}
}

// Reset resets the machine to its initial state.
func (f *FSM) Reset() {
	f.current = ""
}

// Current returns the current state.
func (f *FSM) State() *state {
	return f.states[f.current]
}

// Enter sets a func that will be called when entering any state.
func (f *FSM) AddListener(fn func(Trace)) int {
	index := len(f.listeners)
	f.listeners[index] = fn
	return index
}

func (f *FSM) RemoveListener(index int) {
	delete(f.listeners, index)
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

func (f *FSM) notify(trace Trace) bool {
	if f == nil {
		return false
	}
	for _, listener := range f.listeners {
		listener(trace)
	}
	return true
}

func (fsm *FSM) transition(source *state, transition *transition, event Event, data any) {
	if fsm == nil {
		return
	}
	target, ok := fsm.states[transition.target]
	if ok || target == source {
		source.leave(fsm, event, data)
	}
	// if effect != nil {
	transition.effect.execute(fsm, event, data)
	transition.effect.wait()
	// }
	fsm.notify(Trace{
		Kind:         "transition",
		Event:        string(event),
		CurrentState: source.name,
		TargetState:  transition.target,
		Data:         data,
	})
	if ok {
		fsm.current = transition.target
		target.enter(fsm, event, data)
	}
}

// Event send an Event to a machine, applying at most one transition.
// true is returned if a transition has been applied, false otherwise.
func (fsm *FSM) Dispatch(event Event, data any) bool {
	if fsm == nil {
		return false
	}
	fsm.wait()
	source, ok := fsm.states[fsm.current]
	if !ok {
		return false
	}

	if source.submachine != nil && fsm.submachineState != source && source.submachine.Dispatch(event, data) {
		return true
	}
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()
	for _, transition := range source.transitions {
		_, ok := find(transition.events, func(evt Event) bool {
			match, err := path.Match(string(evt), string(event))
			if err != nil {
				slog.Warn("Error matching event", "error", err)
				return false
			}
			return match
		})
		if !ok {
			continue
		}
		if !transition.guard.evaluate(fsm, event, data) {
			continue
		}
		fsm.transition(source, transition, event, data)
		return true
	}
	fsm.notify(Trace{
		Kind:         "dispatch",
		Event:        string(event),
		CurrentState: source.name,
		Data:         data,
	})
	return false
}

func (fsm *FSM) Context() Context {
	return Context{
		FSM:     fsm,
		Context: fsm.ctx,
	}
}

func Model(elements ...PartialElement) *Modeled {
	newModel := &Modeled{
		behavior: &behavior{
			mutex: &sync.Mutex{},
		},
		states: map[string]*state{
			"": {
				name:        "",
				transitions: []*transition{},
			},
		},
	}
	for _, partial := range elements {
		partial(newModel, nil, nil)
	}
	newModel.behavior.action = func(ctx Context, event Event, data any) {
		initial, ok := newModel.states[""]
		if !ok {
			slog.Warn("No initial state found")
			return
		}
		transition := initial.transitions[0]
		if transition == nil {
			slog.Warn("No initial transition found")
			return
		}
		ctx.transition(initial, transition, event, data)
	}
	return newModel
}
