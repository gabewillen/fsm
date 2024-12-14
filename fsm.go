package fsm

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"
)

// required for type guard
type node interface{}

type Context struct {
	*FSM
	context.Context
}

func (ctx *Context) Broadcast(event Event, data any) {
	machines, ok := ctx.Value(broadcastKey).(map[int]*FSM)
	if !ok {
		slog.Warn("[fsm][Broadcast] no machines found in context")
		return
	}
	for _, machine := range machines {
		go machine.Dispatch(event, data)
	}
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

type Path string
type Event string
type Action func(Context, Event, any)
type Constraint func(Context, Event, any) bool

type constraint struct {
	expr Constraint
}

const (
	InitialPath Path = ".initial"
	AnyPath     Path = "*"
)

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

func (element *behavior) execute(fsm *FSM, event Event, data any) {
	if element == nil || element.action == nil {
		return
	}
	element.execution.Add(1)
	var ctx context.Context
	ctx, element.cancel = context.WithCancel(fsm.ctx)
	go func() {
		element.action(Context{FSM: fsm, Context: ctx}, event, data)
		element.execution.Done()
	}()
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

type kind string

const (
	StateKind    kind = "State"
	ChoiceKind   kind = "Choice"
	InitialKind  kind = "Initial"
	FinalKind    kind = "Final"
	ExternalKind kind = "External"
	LocalKind    kind = "Local"
	InternalKind kind = "Internal"
	SelfKind     kind = "Self"
)

type state struct {
	path        Path
	kind        kind
	entry       *behavior
	activity    *behavior
	exit        *behavior
	transitions []*transition
	submachine  *FSM
}

func (statePtr *state) enter(fsm *FSM, event Event, data any) {
	slog.Debug("[state][enter] enter", "state", statePtr, "event", event, "data", data)
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
	slog.Debug("[state][leave] leave", "state", statePtr.path, "event", event, "data", data)
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
	return string(element.path)
}

func (element *state) Submachine() *FSM {
	if element == nil {
		return nil
	}
	return element.submachine
}

type transition struct {
	events []Event
	kind   kind
	guard  *constraint
	effect *behavior
	target Path
	source Path
}

type Modeled struct {
	*behavior
	states          map[Path]*state
	current         Path
	submachineState *state
}

type builderContext struct {
	state      *state
	transition *transition
}

type ModelBuilder struct {
	*Modeled
	state      *state
	transition *transition
	stack      []builderContext
}

func (builder *ModelBuilder) push(state *state, transition *transition) *ModelBuilder {
	builder.stack = append(builder.stack, builderContext{
		state:      state,
		transition: transition,
	})
	builder.state = state
	builder.transition = transition
	return builder
}

func (builder *ModelBuilder) pop() *ModelBuilder {
	builder.stack = builder.stack[:len(builder.stack)-1]
	if len(builder.stack) > 0 {
		builder.state = builder.stack[len(builder.stack)-1].state
		builder.transition = builder.stack[len(builder.stack)-1].transition
	} else {
		builder.state = nil
		builder.transition = nil
	}
	return builder
}

// type model struct {
// 	*statemachine
// }

type Trace struct {
	Kind         string
	Event        string
	CurrentState Path
	TargetState  Path
	Data         any
}

// FSM is a finite state machine.
type FSM struct {
	*Modeled
	ctx       Context
	listeners map[int]func(Trace)
}

type Buildable func(*ModelBuilder)

var broadcastKey = Context{}

// New creates a new finite state machine having the specified initial state.
func New(ctx context.Context, model *ModelBuilder) *FSM {
	machines, ok := ctx.Value(broadcastKey).(map[int]*FSM)
	if !ok {
		machines = map[int]*FSM{}
	}
	fsm := &FSM{
		Modeled: &Modeled{
			behavior: &behavior{
				action:    model.behavior.action,
				execution: sync.WaitGroup{},
				mutex:     &sync.Mutex{},
			},
			states: model.states,
		},
		listeners: map[int]func(Trace){},
	}
	machines[len(machines)] = fsm
	fsm.ctx = Context{Context: context.WithValue(ctx, broadcastKey, machines), FSM: fsm}
	fsm.Modeled.behavior.execute(fsm, "", nil)
	return fsm
}

func asPath(id string) Path {
	cleanedPath := path.Clean(id)

	if strings.HasPrefix(cleanedPath, ".") && !strings.HasPrefix(cleanedPath, "..") {
		slog.Warn("[fsm] Path cannot start with a single dot, this will be removed and will likely result in an invalid model", "path", id)
		cleanedPath = cleanedPath[1:]
	}
	return Path(cleanedPath)
}

func Initial[T Targetable](name T, partialElements ...Buildable) Buildable {
	return func(model *ModelBuilder) {
		currentPath := Path("")
		if model.state != nil {
			currentPath = model.state.path
		}
		switch any(name).(type) {
		case string, Path:
			id := asPath(any(name).(string))
			initialPath := Path(path.Join(string(currentPath), string(InitialPath)))
			initial, ok := model.states[initialPath]
			if !ok {
				initial = &state{
					path:        initialPath,
					kind:        InitialKind,
					transitions: []*transition{},
				}
				model.states[initialPath] = initial
			}
			targetPath := Path(path.Join(string(currentPath), string(id)))
			target, ok := model.states[targetPath]
			if !ok {
				target = &state{
					path:        targetPath,
					kind:        StateKind,
					transitions: []*transition{},
				}
				model.states[targetPath] = target
			}
			transition := &transition{
				events: []Event{},
				kind:   ExternalKind,
				source: initialPath,
				target: target.path,
			}
			model.push(target, transition)
			for _, partial := range partialElements {
				partial(model)
			}
			model.pop()
			initial.transitions = append(initial.transitions, transition)
		case Buildable:
			partial := any(name).(Buildable)
			partial(model)
		}
	}
}

func State(name string, partialElements ...Buildable) Buildable {
	statePath := asPath(name)
	return func(model *ModelBuilder) {
		if model.state != nil {
			statePath = Path(path.Join(string(model.state.path), string(statePath)))
		}
		stateElement, ok := model.states[statePath]
		if !ok {
			stateElement = &state{
				path:        statePath,
				kind:        StateKind,
				transitions: []*transition{},
			}
			model.states[statePath] = stateElement
		}
		model.push(stateElement, nil)
		for _, partial := range partialElements {
			partial(model)
		}
		model.pop()
	}
}

func Entry(action Action) Buildable {
	return func(model *ModelBuilder) {
		if model.state == nil {
			slog.Warn("[fsm][Entry] called outside of a state, Entry can only be used inside of fsm.State(...)")
			return
		}
		model.state.entry = &behavior{
			action: action,
		}
	}
}

func Activity(fn Action) Buildable {
	return func(model *ModelBuilder) {
		if model.state == nil {
			slog.Warn("[fsm][Activity] called outside of a state, Activity can only be used inside of fsm.State(...)")
			return
		}
		model.state.activity = &behavior{
			action: fn,
		}
	}
}

func Exit(action Action) Buildable {
	return func(model *ModelBuilder) {
		if model.state == nil {
			slog.Warn("[fsm][Exit] called outside of a state, Exit can only be used inside of fsm.State(...)")
			return
		}
		model.state.exit = &behavior{
			action: action,
		}
	}
}

// Src defines the source States for a Transition.
func Source(sourcePath Path) Buildable {
	return func(model *ModelBuilder) {
		if model.transition == nil {
			slog.Warn("[fsm][Source] called outside of a transition, Source can only be used inside of Transition(...)")
			return
		}
		sourcePath = asPath(string(sourcePath))
		if model.state != nil {
			sourcePath = Path(path.Join(string(model.state.path), string(sourcePath)))
		}
		source, ok := model.states[sourcePath]
		if !ok {
			source = &state{
				path:        sourcePath,
				kind:        StateKind,
				transitions: []*transition{},
			}
			model.states[sourcePath] = source
		}
		source.transitions = append(source.transitions, model.transition)
		model.transition.source = sourcePath
	}
}

type Dispatchable interface {
	string | node | Buildable
}

// On defines the Event that triggers a Transition.
func On[E Dispatchable](events ...E) Buildable {
	return func(model *ModelBuilder) {
		if model.transition == nil {
			slog.Warn("[fsm][On] called outside of a transition, On can only be used inside of fsm.Transition(...)")
			return
		}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				model.transition.events = append(model.transition.events, Event(any(evt).(string)))
			case Event:
				model.transition.events = append(model.transition.events, any(evt).(Event))
			case Buildable:
				any(evt).(Buildable)(model)
			}
		}
	}
}

type Targetable interface {
	string | Buildable | Path
}

// Dst defines the new State the machine switches to after a Transition.
func Target[T Targetable](target T) Buildable {
	return func(model *ModelBuilder) {
		if model.transition == nil {
			slog.Warn("[fsm][Target] called outside of a transition, Target can only be used inside of Transition(...)")
			return
		}
		switch any(target).(type) {
		case string, Path:
			targetPath := asPath(any(target).(string))
			if model.state != nil {
				targetPath = Path(path.Join(string(model.state.path), string(targetPath)))
			}
			if _, ok := model.states[targetPath]; !ok {
				slog.Debug("[fsm][Target] creating target state", "path", targetPath)
				model.states[targetPath] = &state{
					path:        targetPath,
					kind:        StateKind,
					transitions: []*transition{},
				}
			}
			model.transition.target = targetPath
		case Buildable:
			any(target).(Buildable)(model)
		}
	}
}

func Choice(transitions ...Buildable) Buildable {
	return func(model *ModelBuilder) {
		choicePath := Path("")
		if model.state != nil {
			choicePath = Path(path.Join(string(model.state.path), string(choicePath)))
		}

		choice := &state{
			path:        choicePath,
			kind:        ChoiceKind,
			transitions: []*transition{},
		}
		model.push(choice, nil)
		for _, transition := range transitions {
			transition(model)
		}
		model.pop()
		choice.path = Path(fmt.Sprintf("%s/choice-%d", choice.path, len(model.states)))
		model.states[choice.path] = choice
		model.transition.target = choice.path
		for _, transition := range choice.transitions {
			transition.source = choice.path
		}

	}
}

func Guard(fn Constraint) Buildable {
	return func(model *ModelBuilder) {
		if model.transition == nil {
			slog.Warn("[fsm][Guard] called outside of a transition, Guard can only be used inside of Transition(...)")
			return
		}
		model.transition.guard = &constraint{
			expr: fn,
		}
	}
}

func Submachine(submachine *ModelBuilder) Buildable {
	return func(model *ModelBuilder) {
		if model.state == nil {
			slog.Warn("[fsm][Submachine] called outside of a state, Submachine can only be used inside of State(...)")
			return
		}
		model.state.submachine = &FSM{
			Modeled: &Modeled{
				behavior: &behavior{
					action:    submachine.behavior.action,
					execution: sync.WaitGroup{},
					mutex:     model.behavior.mutex,
				},
				states:          submachine.states,
				submachineState: model.state,
			},
			listeners: map[int]func(Trace){},
		}
	}
}

func Effect(fn Action) Buildable {
	return func(model *ModelBuilder) {
		if model.transition == nil {
			slog.Warn("[fsm][Effect] called outside of a transition, Effect can only be used inside of Transition(...)")
			return
		}
		model.transition.effect = &behavior{
			action: fn,
		}
	}
}

func Transition(nodes ...Buildable) Buildable {
	return func(model *ModelBuilder) {
		transition := &transition{}
		model.push(model.state, transition)
		for _, node := range nodes {
			node(model)
		}
		model.pop()
		slog.Debug("[fsm][Transition] transition", "transition", transition)
		if model.state != nil {
			transition.source = model.state.path
			model.state.transitions = append(model.state.transitions, transition)
		} else if transition.target == "" {
			slog.Error("[fsm][Transition] target is empty", "transition", transition)
		}
		if transition.target == transition.source {
			transition.kind = SelfKind
		} else if transition.target == "" {
			transition.kind = InternalKind
		} else if match, err := path.Match(string(transition.source)+"/*", string(transition.target)); err == nil && match {
			transition.kind = LocalKind
		} else {
			transition.kind = ExternalKind
		}
	}
}

// Reset resets the machine to its initial state.
func (f *FSM) Reset() {
	f.current = ""
}

// Current returns the current state.
func (fsm *FSM) State() *state {
	if fsm == nil {
		return nil
	}
	fsm.wait()
	return fsm.states[fsm.current]
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

func (f *FSM) notify(trace Trace) bool {
	if f == nil {
		return false
	}
	for _, listener := range f.listeners {
		listener(trace)
	}
	return true
}

func (fsm *FSM) transition(current *state, transition *transition, event Event, data any) (any, bool) {
	var ok bool
	var target *state
	if fsm == nil {
		return nil, false
	}
	for transition != nil {
		fsm.notify(Trace{
			Kind:         "transition",
			Event:        string(event),
			CurrentState: current.path,
			TargetState:  transition.target,
			Data:         data,
		})
		switch transition.kind {
		case InternalKind:
			transition.effect.execute(fsm, event, data)
			transition.effect.wait()
			return data, true
		case SelfKind, ExternalKind:

			exit := strings.Split(string(current.path), "/")
			for index := range exit {
				current, ok = fsm.states[Path(path.Join(exit[:len(exit)-index]...))]
				if ok {
					current.leave(fsm, event, data)
				}
			}
		}
		transition.effect.execute(fsm, event, data)
		transition.effect.wait()
		var enter []string
		if transition.kind == SelfKind {
			enter = []string{string(transition.target)}
		} else {
			enter = strings.Split(strings.TrimPrefix(string(transition.target), string(transition.source)), "/")
		}
		slog.Debug("[fsm][transition] enter", "enter", enter)
		for index := range enter {
			current, ok = fsm.states[Path(path.Join(enter[:index+1]...))]
			if ok {
				current.enter(fsm, event, data)
			}
		}
		if target, ok = fsm.states[transition.target]; ok && target != nil {
			if target.kind == ChoiceKind {
				current = target
				for _, choice := range target.transitions {
					if !choice.guard.evaluate(fsm, event, data) {
						continue
					}
					transition = choice
					break
				}
				continue
			} else {
				fsm.current = transition.target
				fsm.initial(target, event, data)
				return data, true
			}
		}
		return data, true
	}
	return nil, false
}

func (fsm *FSM) initial(state *state, event Event, data any) {
	if fsm == nil {
		return
	}
	initialPath := InitialPath
	if state != nil {
		initialPath = Path(path.Join(string(state.path), string(InitialPath)))
	}
	initial, ok := fsm.states[initialPath]
	if !ok {
		if state == nil {
			slog.Warn("[FSM][initial] No initial state found", "path", initialPath)
		}
		return
	}
	transition := initial.transitions[0]
	if transition == nil {
		slog.Warn("[FSM][initial] No initial transition found", "path", initialPath)
		return
	}
	fsm.transition(initial, transition, event, data)
}

// Event send an Event to a machine, applying at most one transition.
// true is returned if a transition has been applied, false otherwise.
func (fsm *FSM) Dispatch(event Event, data any) (any, bool) {
	if fsm == nil {
		return nil, false
	}
	fsm.wait()

	current, ok := fsm.states[fsm.current]
	if !ok {
		return nil, false
	}
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()
	states := strings.Split(string(fsm.current), "/")
	for index := range states {
		source, ok := fsm.states[Path(path.Join(states[:index+1]...))]
		if !ok {
			return nil, false
		}
		if source.submachine != nil && fsm.submachineState != source {
			if result, ok := source.submachine.Dispatch(event, data); ok {
				return result, ok
			}
		}
		for _, transition := range source.transitions {
			index := slices.IndexFunc(transition.events, func(evt Event) bool {
				match, err := path.Match(string(evt), string(event))
				if err != nil {
					slog.Warn("Error matching event", "error", err)
					return false
				}
				return match
			})
			if index == -1 {
				continue
			}
			if !transition.guard.evaluate(fsm, event, data) {
				continue
			}
			return fsm.transition(current, transition, event, data)
		}
	}

	fsm.notify(Trace{
		Kind:         "dispatch",
		Event:        string(event),
		CurrentState: current.path,
		Data:         data,
	})
	return nil, false
}

func (fsm *FSM) Context() *Context {
	return &fsm.ctx
}

func Model(elements ...Buildable) *ModelBuilder {
	builder := &ModelBuilder{
		Modeled: &Modeled{
			behavior: &behavior{
				mutex: &sync.Mutex{},
			},
			states: map[Path]*state{
				InitialPath: {
					kind:        StateKind,
					path:        InitialPath,
					transitions: []*transition{},
				},
			},
		},
	}
	for _, buildable := range elements {
		buildable(builder)
	}
	builder.Modeled.behavior.action = func(ctx Context, event Event, data any) {
		ctx.initial(nil, event, data)
	}
	return builder
}
