package fsm

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
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

type element[T any] interface {
}

type Element = func(builder *Builder)

/******************** Events ********************/

type Event interface {
	element[event]
	Id() string
	Kind() Kind
	Data() any
	WithData(data any) Event
}

type event struct {
	Event
	kind Kind
	data any
	id   uuid.UUID
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

func (e *event) Id() string {
	if e.id == uuid.Nil {
		return ""
	}
	return e.id.String()
}

func (e *event) WithData(data any) Event {
	uuid, err := uuid.NewV7()
	if err != nil {
		slog.Error("[fsm][event][WithData] failed to create new event", "error", err)
	}
	return &event{kind: e.kind, data: data, id: uuid}
}

func NewEvent(kind Kind, data any) Event {
	return &event{kind: kind, data: data}
}

func EventKind(kind Kind) Event {
	return NewEvent(kind, nil)
}

var AnyEvent = EventKind("*")

/******************** Ref ********************/

type Ref = any

/******************** Context ********************/

type Context struct {
	context.Context
	*process
	Ref Ref
}

/******************** Constraints ********************/

type Expression func(Context, Event) bool

type constraint struct {
	expr Expression
}

/******************** Behavior ********************/

type Action func(Context, Event)

type behavior struct {
	*process
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
}

func (state *state) Path() Path {
	return state.path
}

/******************** Transitions ********************/

type transition struct {
	events []Kind
	kind   Kind
	guard  *constraint
	effect *behavior
	target Path
	source Path
}

/******************** Model ********************/

// type Model interface {
// }

type Model struct {
	*behavior
	states          map[Path]*state
	submachineState *state
	storage         any
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
	// context.Context
	channel chan struct{}
	cancel  context.CancelFunc
}

var inactive = func() *active {
	_, cancel := context.WithCancel(context.Background())
	cancel()
	channel := make(chan struct{})
	close(channel)
	return &active{
		channel: channel,
		cancel:  cancel,
	}
}()

type process struct {
	context.Context
	model  *Model
	state  *state
	ref    any
	active map[*behavior]*active
	mutex  *sync.Mutex
}

var empty = struct{}{}

type activeSymbol struct{}

var activeKey = activeSymbol{}

var processPool = sync.Pool{
	New: func() any {
		return &process{
			model: &Model{
				behavior: &behavior{},
				storage:  nil,
			},
			active: map[*behavior]*active{},
			mutex:  &sync.Mutex{},
		}
	},
}

func Execute(ctx context.Context, model *Model) Context {
	process := processPool.Get().(*process)
	process.model.states = model.states
	process.model.submachineState = model.submachineState
	process.model.behavior.action = model.behavior.action
	process.ref = ctx
	if model.submachineState == nil {
		active, ok := ctx.Value(activeKey).(*sync.Map)
		if !ok {
			active = &sync.Map{}
		}
		active.Store(process, empty)
		ctx = context.WithValue(ctx, activeKey, active)
	}
	process.Context = ctx
	process.execute(process.model.behavior, nil, false)
	return Context{
		Context: ctx,
		process: process,
		Ref:     process.ref,
	}
}

func Terminate(ctx *Context) {
	ctx.process.terminate(ctx.process.model.behavior)
	processPool.Put(ctx.process)
	ctx.process = nil
}

func Send[T Sendable](process *process, event T) bool {
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

func (proc *process) Broadcast(event Event) {
	active, ok := proc.Value(activeKey).(*sync.Map)
	if !ok {
		slog.Warn("[fsm][Broadcast] no machines found in context")
		return
	}
	active.Range(func(value any, _ any) bool {
		process, ok := value.(*process)
		if !ok {
			slog.Warn("[fsm][Broadcast] value is not a *Process", "value", value, "process", process)
			return true
		}
		go process.Send(event)
		return true
	})
}

func (process *process) State() Path {
	if process == nil {
		return ""
	}
	process.wait(process.model.behavior)
	if process.state == nil {
		return ""
	}
	return process.state.path
}

func (process *process) evaluate(element *constraint, event Event) bool {
	if element == nil {
		return true
	}
	return element.expr(Context{Context: process, Ref: process.ref}, event)
}

func (process *process) terminate(element *behavior) {
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

func (process *process) exit(state *state, event Event) {
	if state == nil {
		return
	}
	slog.Debug("[state][leave] leave", "state", state.path, "event", event)
	process.terminate(state.activity)
	process.execute(state.exit, event, true)
}

func (process *process) Ref() any {
	return process.ref
}

func (process *process) enter(state *state, event Event) {
	slog.Debug("[state][enter] enter", "state", state, "event", event)
	if state == nil || process == nil || state.kind != StateKind {
		return
	}
	process.execute(state.entry, event, true)
	process.execute(state.activity, event, false)
	// context.AfterFunc(process.execute(state.activity, event, false), func() {
	// 	if process.state == nil || !strings.HasPrefix(string(process.state.path), string(state.path)) {
	// 		return
	// 	}
	// 	substates := strings.Split(string(state.path), "/")
	// 	// TODO: code duplication here
	// 	for index := range substates {
	// 		path := Path(path.Join(substates[:len(substates)-index]...))
	// 		state, ok := process.states[path]
	// 		if !ok {
	// 			slog.Warn("[fsm][execute] state not found", "path", path)
	// 			continue
	// 		}
	// 		select {
	// 		case <-process.Done():
	// 			return
	// 		default:
	// 			process.wait(state.activity)
	// 		}
	// 	}
	// 	// process.Send()
	// })

}

func (process *process) execute(element *behavior, event Event, wait bool) *active {
	if process == nil || element == nil || element.action == nil {
		return inactive
	}
	current, ok := process.active[element]
	if current == nil || !ok {
		current = &active{}
		process.active[element] = current
	}
	var ctx context.Context
	ctx, current.cancel = context.WithCancel(process)
	current.channel = make(chan struct{})
	go func() {
		element.action(Context{Context: ctx, process: process, Ref: process.ref}, event)
		close(current.channel)
	}()
	if wait {
		<-current.channel
	}
	return current
}

func (process *process) transition(current *state, transition *transition, event Event) *state {
	slog.Debug("[fsm][transition] transition", "transition", transition, "current", current, "event", event)
	var ok bool
	var target *state
	if process == nil {
		return nil
	}
	for transition != nil {
		switch transition.kind {
		case InternalKind:
			process.execute(transition.effect, event, true)
			return current
		case SelfKind, ExternalKind:
			exit := strings.Split(string(current.path), "/")
			for index := range exit {
				current, ok = process.model.states[Path(path.Join(exit[:len(exit)-index]...))]
				if ok {
					process.exit(current, event)
				}
			}
		}
		process.execute(transition.effect, event, true)
		var enter []string
		if transition.kind == SelfKind {
			enter = []string{string(transition.target)}
		} else {
			enter = strings.Split(strings.TrimPrefix(string(transition.target), string(transition.source)), "/")
		}
		for index := range enter {
			current, ok = process.model.states[Path(path.Join(enter[:index+1]...))]
			if ok {
				process.enter(current, event)
			}
		}
		if target, ok = process.model.states[transition.target]; ok && target != nil {
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

func (process *process) initial(state *state, event Event) *state {
	if process == nil {
		slog.Error("[FSM][initial] FSM is nil")
		return nil
	}
	initialPath := Path(".initial")
	if state != nil {
		initialPath = Path(path.Join(string(state.path), string(initialPath)))
	}
	initial, ok := process.model.states[initialPath]
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

func (process *process) wait(behavior *behavior) {
	if process == nil {
		return
	}
	active, ok := process.active[behavior]
	if !ok {
		return
	}
	<-active.channel
}

func (process *process) Send(event Event) bool {
	if process == nil {
		slog.Error("[FSM][Dispatch] FSM is nil")
		return false
	}

	process.wait(process.model.behavior)
	process.mutex.Lock()
	defer process.mutex.Unlock()

	if process.state == nil {
		slog.Error("[FSM][Dispatch] Current state is nil")
		return false
	}

	states := strings.Split(string(process.state.path), "/")
	for index := range states {
		source, ok := process.model.states[Path(path.Join(states[:index+1]...))]
		if !ok {
			slog.Error("[FSM][Dispatch] Source state not found", "path", Path(path.Join(states[:index+1]...)))
			return false
		}
		for _, transition := range source.transitions {
			index := slices.IndexFunc(transition.events, func(evt Kind) bool {
				match, err := path.Match(string(evt), string(event.Kind()))
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
			events: []Kind{},
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
		builder.transition.events = []Kind{}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				builder.transition.events = append(builder.transition.events, Kind(any(evt).(string)))
			case Event:
				builder.transition.events = append(builder.transition.events, any(evt).(Event).Kind())
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
			events: []Kind{},
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
		if len(transition.events) == 0 {
			// this is a completion transition
			kind := transition.source + ".completion"
			slog.Info("[fsm][Transition] completion transition", "event", kind)
			transition.events = append(transition.events, Kind(kind))

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
