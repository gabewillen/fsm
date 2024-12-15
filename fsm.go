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

/******************** Path ********************/

type Path string

func pathFromName(name string) Path {
	cleanedPath := path.Clean(name)

	if strings.HasPrefix(cleanedPath, ".") && !strings.HasPrefix(cleanedPath, "..") {
		slog.Warn("[fsm] Path cannot start with a single dot, this will be removed and will likely result in an invalid model", "path", name)
		cleanedPath = cleanedPath[1:]
	}
	return Path(cleanedPath)
}

/******************** Kinds ********************/

type Kind string

const (
	StateKind      Kind = "State"
	ChoiceKind     Kind = "Choice"
	InitialKind    Kind = "Initial"
	FinalKind      Kind = "Final"
	ExternalKind   Kind = "External"
	LocalKind      Kind = "Local"
	InternalKind   Kind = "Internal"
	SelfKind       Kind = "Self"
	TransitionKind Kind = "Transition"
)

/******************** Element ********************/

type element[T any] interface{}

type Element = func(builder *Builder)

/******************** Events ********************/

type Event interface {
	element[event]
	Kind() Kind
	Data() any
}

type event struct {
	Event
	kind Kind
	data any
}

type Sendable interface {
	element[event] | string
}

func (e *event) Kind() Kind {
	return e.kind
}

func (e *event) Data() any {
	return e.data
}

func (e *event) New(data any) Event {
	return &event{kind: e.kind, data: data}
}

func NewEvent(kind Kind, data any) Event {
	return &event{kind: kind, data: data}
}

var AnyEvent = NewEvent(Kind("*"), nil)
var CompletionEvent = NewEvent(Kind(""), nil)

/******************** Context ********************/

type Context struct {
	context.Context
	*Process
}

/******************** Constraints ********************/

type Expression func(Context, Event) bool

type constraint struct {
	expr Expression
}

/******************** Behavior ********************/

type Action func(Context, Event)

type behavior struct {
	Context
	action Action
}

/******************** States ********************/

type state struct {
	_           element[state] // tell compiler to ignore unused
	path        Path
	kind        Kind
	entry       *behavior
	activity    *behavior
	exit        *behavior
	transitions []*transition
	submachine  *Model
}

func (state *state) Path() Path {
	return state.path
}

func (state *state) Submachine() *Model {
	return state.submachine
}

/******************** Transitions ********************/

type transition struct {
	events []Event
	kind   Kind
	guard  *constraint
	effect *behavior
	target Path
	source Path
}

/******************** Model ********************/

type Model struct {
	*behavior
	states          map[Path]*state
	submachineState *state
}

// New creates a new finite state machine having the specified initial state.
func New(elements ...Element) *Model {
	builder := &Builder{
		model: &Model{
			behavior: &behavior{},
			states: map[Path]*state{
				Path(".initial"): {
					kind:        InitialKind,
					path:        Path(".initial"),
					transitions: []*transition{},
				},
			},
		},
	}
	for _, buildable := range elements {
		buildable(builder)
	}
	builder.model.behavior.action = func(ctx Context, event Event) {
		ctx.mutex.Lock()
		defer ctx.mutex.Unlock()
		ctx.state = ctx.initial(nil, event)
	}
	return builder.model
}

/******************** Builder ********************/

type stack struct {
	state      *state
	transition *transition
}

type Builder struct {
	model      *Model
	state      *state
	transition *transition
	stack      []stack
}

func (builder *Builder) push(state *state, transition *transition) *Builder {
	builder.stack = append(builder.stack, stack{
		state:      state,
		transition: transition,
	})
	builder.state = state
	builder.transition = transition
	return builder
}

func (builder *Builder) pop() *Builder {
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

/******************** Process ********************/

type active struct {
	channel chan struct{}
	cancel  context.CancelFunc
}

type Process struct {
	*Model
	state  *state
	active map[*behavior]*active
	mutex  *sync.Mutex
}

var empty = struct{}{}

type activeSymbol struct{}

var activeKey = activeSymbol{}

var processPool = sync.Pool{
	New: func() any {
		return &Process{
			active: map[*behavior]*active{},
			mutex:  &sync.Mutex{},
		}
	},
}

func Execute(ctx context.Context, model *Model) *Process {
	process := processPool.Get().(*Process)
	process.Model = model
	if model.submachineState == nil {
		active, ok := ctx.Value(activeKey).(*sync.Map)
		if !ok {
			active = &sync.Map{}
		}
		active.Store(process, empty)

	}
	process.Context = Context{Context: ctx, Process: process}
	process.execute(process.Model.behavior, nil)
	return process
}

func Terminate(process **Process) {
	(*process).terminate((*process).behavior)
	processPool.Put(*process)
	*process = nil
}

func Send[T Sendable](process *Process, event T) bool {
	if process == nil {
		slog.Error("[FSM][Dispatch] FSM is nil")
		return false
	}
	switch any(event).(type) {
	case Event:
		return process.Send(any(event).(Event))
	case string:
		return process.Send(NewEvent(Kind(any(event).(string)), nil))
	}
	return false
}

func (process *Process) Broadcast(event Event) {
	active, ok := process.Value(activeKey).(*sync.Map)
	if !ok {
		slog.Warn("[fsm][Broadcast] no machines found in context")
		return
	}
	active.Range(func(value any, _ any) bool {
		go value.(*Process).Send(event)
		return true
	})
}

// Current returns the current state.
func (process *Process) State() *state {
	if process == nil {
		return nil
	}
	process.wait(process.behavior)
	return process.state
}

func (process *Process) evaluate(element *constraint, event Event) bool {
	if element == nil {
		return true
	}
	return element.expr(Context{Context: process, Process: process}, event)
}

func (process *Process) terminate(element *behavior) {
	if element == nil {
		return
	}
	active, ok := process.active[element]
	if !ok {
		return
	}
	active.cancel()
	<-active.channel
}

func (process *Process) exit(state *state, event Event) {
	if state == nil {
		return
	}
	slog.Debug("[state][leave] leave", "state", state.path, "event", event)
	if state.submachine != nil {
		Terminate(&state.submachine.Process)
	}
	process.terminate(state.activity)
	<-process.execute(state.exit, event)
}

func (process *Process) enter(state *state, event Event) {
	slog.Debug("[state][enter] enter", "state", state, "event", event)
	if state == nil {
		return
	}
	<-process.execute(state.entry, event)
	process.execute(state.activity, event)
	if state.submachine != nil {
		state.submachine.Process = Execute(process, state.submachine)
		state.submachine.Process.wait(state.submachine.behavior)
	}
}

func (process *Process) execute(element *behavior, event Event) chan struct{} {

	channel := make(chan struct{})
	if process == nil || element == nil || element.action == nil {
		close(channel)
		return channel
	}
	current, ok := process.active[element]
	if current == nil || !ok {
		current = &active{}
		process.active[element] = current
	}
	var ctx context.Context
	ctx, current.cancel = context.WithCancel(process)
	current.channel = channel
	go func() {
		element.action(Context{Context: ctx, Process: process}, event)
		close(current.channel)
		slog.Debug("[fsm][execute] completion event", "event", CompletionEvent)
		process.Send(CompletionEvent)
	}()
	return current.channel
}

func (process *Process) transition(current *state, transition *transition, event Event) *state {
	slog.Debug("[fsm][transition] transition", "transition", transition, "current", current, "event", event)
	var ok bool
	var target *state
	if process == nil {
		return nil
	}
	for transition != nil {
		switch transition.kind {
		case InternalKind:
			<-process.execute(transition.effect, event)
			return current
		case SelfKind, ExternalKind:
			exit := strings.Split(string(current.path), "/")
			for index := range exit {
				current, ok = process.states[Path(path.Join(exit[:len(exit)-index]...))]
				if ok {
					process.exit(current, event)
				}
			}
		}
		<-process.execute(transition.effect, event)
		var enter []string
		if transition.kind == SelfKind {
			enter = []string{string(transition.target)}
		} else {
			enter = strings.Split(strings.TrimPrefix(string(transition.target), string(transition.source)), "/")
		}
		for index := range enter {
			current, ok = process.states[Path(path.Join(enter[:index+1]...))]
			if ok {
				process.enter(current, event)
			}
		}
		if target, ok = process.states[transition.target]; ok && target != nil {
			if target.kind == ChoiceKind {
				current = target
				for _, choice := range target.transitions {
					if !process.evaluate(choice.guard, event) {
						continue
					}

					transition = choice
					break
				}
				continue
			} else {
				return process.initial(target, event)
			}
		}
		return nil
	}
	return nil
}

func (process *Process) initial(state *state, event Event) *state {
	if process == nil {
		slog.Error("[FSM][initial] FSM is nil")
		return nil
	}
	initialPath := Path(".initial")
	if state != nil {
		initialPath = Path(path.Join(string(state.path), string(initialPath)))
	}
	initial, ok := process.states[initialPath]
	if !ok {
		if state == nil {
			slog.Warn("[FSM][initial] No initial state found", "path", initialPath)
		}
		return state
	}
	transition := initial.transitions[0]
	if transition == nil {
		slog.Warn("[FSM][initial] No initial transition found", "path", initialPath)
		return state
	}
	return process.transition(initial, transition, event)
}

func (process *Process) wait(behavior *behavior) {
	if process == nil {
		return
	}
	active, ok := process.active[behavior]
	if !ok {
		return
	}
	<-active.channel
}

func (process *Process) Send(event Event) bool {
	if process == nil {
		slog.Error("[FSM][Dispatch] FSM is nil")
		return false
	}

	process.wait(process.behavior)
	process.mutex.Lock()
	defer process.mutex.Unlock()

	if process.state == nil {
		slog.Error("[FSM][Dispatch] Current state is nil")
		return false
	}

	states := strings.Split(string(process.state.path), "/")
	for index := range states {
		source, ok := process.states[Path(path.Join(states[:index+1]...))]
		if !ok {
			slog.Error("[FSM][Dispatch] Source state not found", "path", Path(path.Join(states[:index+1]...)))
			return false
		}
		if source.submachine != nil && process.submachineState != source && source.submachine.Send(event) {
			return true
		}
		for _, transition := range source.transitions {
			index := slices.IndexFunc(transition.events, func(evt Event) bool {
				match, err := path.Match(string(evt.Kind()), string(event.Kind()))
				if err != nil {
					slog.Warn("Error matching event", "error", err)
					return false
				}
				return match
			})
			if index == -1 {
				continue
			}
			if !process.evaluate(transition.guard, event) {
				continue
			}
			process.state = process.transition(process.state, transition, event)
			return true
		}
	}
	return false
}

/******************** Elements ********************/

func Initial[T Targetable](name T, elements ...Element) Element {
	return func(builder *Builder) {
		currentPath := Path("")
		if builder.state != nil {
			currentPath = builder.state.path
		}
		initialPath := Path(path.Join(string(currentPath), ".initial"))
		initial, ok := builder.model.states[initialPath]
		if !ok {
			initial = &state{
				path:        Path(".initial"),
				kind:        InitialKind,
				transitions: []*transition{},
			}
			builder.model.states[initialPath] = initial
		}
		initialTransition := &transition{
			events: []Event{},
			kind:   ExternalKind,
			source: initialPath,
		}
		var target *state
		switch any(name).(type) {
		case string, Path:
			targetPath := Path(path.Join(string(currentPath), string(pathFromName(any(name).(string)))))
			target, ok = builder.model.states[targetPath]
			if !ok {
				target = &state{
					path:        targetPath,
					kind:        StateKind,
					transitions: []*transition{},
				}
				builder.model.states[targetPath] = target
			}
			initialTransition.target = targetPath

		case Element:
			element := any(name).(Element)
			elements = append(elements, element)
		}
		builder.push(target, initialTransition)
		for _, element := range elements {
			element(builder)
		}
		builder.pop()
		initial.transitions = append(initial.transitions, initialTransition)
	}
}

func State(name string, elements ...Element) Element {
	statePath := pathFromName(name)
	return func(builder *Builder) {
		if builder.state != nil {
			statePath = Path(path.Join(string(builder.state.path), string(statePath)))
		}
		stateElement, ok := builder.model.states[statePath]
		if !ok {
			stateElement = &state{
				path:        statePath,
				kind:        StateKind,
				transitions: []*transition{},
			}
			builder.model.states[statePath] = stateElement
		}
		builder.push(stateElement, nil)
		for _, element := range elements {
			element(builder)
		}
		builder.pop()
	}
}

func Entry(action Action) Element {
	return func(builder *Builder) {
		if builder.state == nil {
			slog.Warn("[fsm][Entry] called outside of a state, Entry can only be used inside of fsm.State(...)")
			return
		}
		builder.state.entry = &behavior{
			action: action,
		}
	}
}

func Activity(fn Action) Element {
	return func(builder *Builder) {
		if builder.state == nil {
			slog.Warn("[fsm][Activity] called outside of a state, Activity can only be used inside of fsm.State(...)")
			return
		}
		builder.state.activity = &behavior{
			action: fn,
		}
	}
}

func Exit(action Action) Element {
	return func(builder *Builder) {
		if builder.state == nil {
			slog.Warn("[fsm][Exit] called outside of a state, Exit can only be used inside of fsm.State(...)")
			return
		}
		builder.state.exit = &behavior{
			action: action,
		}
	}
}

// Src defines the source States for a Transition.
func Source(sourcePath Path) Element {
	return func(builder *Builder) {
		if builder.transition == nil {
			slog.Warn("[fsm][Source] called outside of a transition, Source can only be used inside of Transition(...)")
			return
		}
		sourcePath = pathFromName(string(sourcePath))
		if builder.state != nil {
			sourcePath = Path(path.Join(string(builder.state.path), string(sourcePath)))
		}
		source, ok := builder.model.states[sourcePath]
		if !ok {
			source = &state{
				path:        sourcePath,
				kind:        StateKind,
				transitions: []*transition{},
			}
			builder.model.states[sourcePath] = source
		}
		source.transitions = append(source.transitions, builder.transition)
		builder.transition.source = sourcePath
	}
}

// On defines the Event that triggers a Transition.
func On[E Sendable](events ...E) Element {
	return func(builder *Builder) {
		if builder.transition == nil {
			slog.Warn("[fsm][On] called outside of a transition, On can only be used inside of fsm.Transition(...)")
			return
		}
		builder.transition.events = []Event{}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				builder.transition.events = append(builder.transition.events, &event{kind: Kind(any(evt).(string))})
			case Event:
				builder.transition.events = append(builder.transition.events, any(evt).(Event))
			}
		}
	}
}

type Targetable interface {
	string | Element | Path | element[state]
}

// Dst defines the new State the machine switches to after a Transition.
func Target[T Targetable](target T) Element {
	return func(builder *Builder) {
		if builder.transition == nil {
			slog.Warn("[fsm][Target] called outside of a transition, Target can only be used inside of Transition(...)")
			return
		}
		switch any(target).(type) {
		case string, Path:
			targetPath := pathFromName(any(target).(string))
			if builder.state != nil {
				targetPath = Path(path.Join(string(builder.state.path), string(targetPath)))
			}
			if _, ok := builder.model.states[targetPath]; !ok {
				slog.Debug("[fsm][Target] creating target state", "path", targetPath)
				builder.model.states[targetPath] = &state{
					path:        targetPath,
					kind:        StateKind,
					transitions: []*transition{},
				}
			}
			builder.transition.target = targetPath
		case Element:
			any(target).(Element)(builder)
		}
	}
}

func Choice(transitions ...Element) Element {
	return func(builder *Builder) {
		choicePath := Path("")
		if builder.state != nil {
			choicePath = Path(path.Join(string(builder.state.path), string(choicePath)))
		}

		choice := &state{
			path:        choicePath,
			kind:        ChoiceKind,
			transitions: []*transition{},
		}
		builder.push(choice, nil)
		for _, transition := range transitions {
			transition(builder)
		}
		builder.pop()
		choice.path = Path(fmt.Sprintf("%s/choice-%d", choice.path, len(builder.model.states)))
		builder.model.states[choice.path] = choice
		builder.transition.target = choice.path
		for _, transition := range choice.transitions {
			transition.source = choice.path
		}

	}
}

func Guard(expr Expression) Element {
	return func(builder *Builder) {
		if builder.transition == nil {
			slog.Warn("[fsm][Guard] called outside of a transition, Guard can only be used inside of Transition(...)")
			return
		}
		builder.transition.guard = &constraint{
			expr: expr,
		}
	}
}

func Submachine(model *Model) Element {
	return func(builder *Builder) {
		if builder.state == nil {
			slog.Warn("[fsm][Submachine] called outside of a state, Submachine can only be used inside of State(...)")
			return
		}
		submachine := &Model{
			behavior: &behavior{
				action: model.behavior.action,
			},
			states:          model.states,
			submachineState: builder.state,
		}
		builder.state.submachine = submachine
	}
}

func Effect(fn Action) Element {
	return func(model *Builder) {
		if model.transition == nil {
			slog.Warn("[fsm][Effect] called outside of a transition, Effect can only be used inside of Transition(...)")
			return
		}
		model.transition.effect = &behavior{
			action: fn,
		}
	}
}

func Transition(elements ...Element) Element {
	return func(builder *Builder) {
		transition := &transition{
			events: []Event{CompletionEvent},
		}
		builder.push(builder.state, transition)
		for _, element := range elements {
			element(builder)
		}
		builder.pop()
		if builder.state != nil && transition.source == "" {
			transition.source = builder.state.path
			builder.state.transitions = append(builder.state.transitions, transition)
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
