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

type Dispatchable interface {
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

/******************** process ********************/

type Context struct {
	context.Context
	*FSM
}

/******************** Constraints ********************/

type Expression func(Context, Event) bool

type constraint struct {
	expr Expression
}

/******************** Behavior ********************/

type Action func(Context, Event)

type behavior struct {
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

type model struct {
	*behavior
	states          map[Path]*state
	submachineState *state
	storage         any
}

// New creates a new finite state machine having the specified initial state.
func Model(elements ...Element) *model {
	builder := &Builder{
		model: &model{
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
	model      *model
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

/******************** fsm ********************/

type active struct {
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

type FSM struct {
	*model
	context.Context
	state  *state
	active map[*behavior]*active
	mutex  *sync.Mutex
}

var empty = struct{}{}

type activeSymbol struct{}

var activeKey = activeSymbol{}

var fsmPool = sync.Pool{
	New: func() any {
		return &FSM{
			model: &model{
				behavior: &behavior{},
				storage:  nil,
			},
			active: map[*behavior]*active{},
			mutex:  &sync.Mutex{},
		}
	},
}

func New(ctx context.Context, model *model) *FSM {
	fsm := fsmPool.Get().(*FSM)
	fsm.states = model.states
	fsm.submachineState = model.submachineState
	fsm.behavior.action = model.behavior.action
	active, ok := ctx.Value(activeKey).(*sync.Map)
	if !ok {
		active = &sync.Map{}
	}
	active.Store(fsm, empty)
	fsm.Context = context.WithValue(ctx, activeKey, active)
	fsm.execute(fsm.behavior, nil, false)
	return fsm
}

func Delete(fsm **FSM) {
	(*fsm).terminate((*fsm).behavior)
	fsmPool.Put(*fsm)
	*fsm = nil
}

func Dispatch[T Dispatchable](fsm *FSM, event T) bool {
	if fsm == nil {
		slog.Error("[process][Dispatch] process is nil")
		return false
	}
	switch any(event).(type) {
	case Event:
		return fsm.Dispatch(any(event).(Event))
	case string:
		return fsm.Dispatch(NewEvent(Kind(any(event).(string)), nil))
	}
	return false
}

func (fsm *FSM) DispatchAll(event Event) {
	active, ok := fsm.Value(activeKey).(*sync.Map)
	if !ok {
		slog.Warn("[fsm][Broadcast] no machines found in context")
		return
	}
	active.Range(func(value any, _ any) bool {
		fsm, ok := value.(*FSM)
		if !ok {
			slog.Warn("[fsm][Broadcast] value is not a *fsm", "value", value, "fsm", fsm)
			return true
		}
		go fsm.Dispatch(event)
		return true
	})
}

func (fsm *FSM) State() Path {
	if fsm == nil {
		return ""
	}
	fsm.wait(fsm.model.behavior)
	if fsm.state == nil {
		return ""
	}
	return fsm.state.path
}

func (fsm *FSM) evaluate(element *constraint, event Event) bool {
	if element == nil {
		return true
	}
	return element.expr(Context{
		Context: fsm,
		FSM:     fsm,
	}, event)
}

func (fsm *FSM) terminate(element *behavior) {
	if element == nil {
		return
	}
	active, ok := fsm.active[element]
	if !ok {
		return
	}
	active.cancel()
	<-active.channel
}

func (fsm *FSM) exit(state *state, event Event) {
	if state == nil {
		return
	}
	slog.Debug("[state][leave] leave", "state", state.path, "event", event)
	fsm.terminate(state.activity)
	fsm.execute(state.exit, event, true)
}

func (fsm *FSM) enter(state *state, event Event) {
	slog.Debug("[state][enter] enter", "state", state, "event", event)
	if state == nil || fsm == nil || state.kind != StateKind {
		return
	}
	fsm.execute(state.entry, event, true)
	fsm.execute(state.activity, event, false)
	// context.AfterFunc(fsm.execute(state.activity, event, false), func() {
	// 	if fsm.state == nil || !strings.HasPrefix(string(fsm.state.path), string(state.path)) {
	// 		return
	// 	}
	// 	substates := strings.Split(string(state.path), "/")
	// 	// TODO: code duplication here
	// 	for index := range substates {
	// 		path := Path(path.Join(substates[:len(substates)-index]...))
	// 		state, ok := fsm.states[path]
	// 		if !ok {
	// 			slog.Warn("[fsm][execute] state not found", "path", path)
	// 			continue
	// 		}
	// 		select {
	// 		case <-fsm.Done():
	// 			return
	// 		default:
	// 			fsm.wait(state.activity)
	// 		}
	// 	}
	// 	// fsm.Dispatch()
	// })

}

func (fsm *FSM) execute(element *behavior, event Event, wait bool) *active {
	if fsm == nil || element == nil || element.action == nil {
		return inactive
	}
	current, ok := fsm.active[element]
	if current == nil || !ok {
		current = &active{}
		fsm.active[element] = current
	}
	var ctx context.Context
	ctx, current.cancel = context.WithCancel(fsm)
	current.channel = make(chan struct{})
	go func() {
		element.action(Context{
			FSM:     fsm,
			Context: ctx,
		}, event)
		close(current.channel)
	}()
	if wait {
		<-current.channel
	}
	return current
}

func (fsm *FSM) transition(current *state, transition *transition, event Event) *state {
	slog.Debug("[fsm][transition] transition", "transition", transition, "current", current, "event", event)
	var ok bool
	var target *state
	if fsm == nil {
		return nil
	}
	for transition != nil {
		switch transition.kind {
		case InternalKind:
			fsm.execute(transition.effect, event, true)
			return current
		case SelfKind, ExternalKind:
			exit := strings.Split(string(current.path), "/")
			for index := range exit {
				current, ok = fsm.states[Path(path.Join(exit[:len(exit)-index]...))]
				if ok {
					fsm.exit(current, event)
				}
			}
		}
		fsm.execute(transition.effect, event, true)
		var enter []string
		if transition.kind == SelfKind {
			enter = []string{string(transition.target)}
		} else {
			enter = strings.Split(strings.TrimPrefix(string(transition.target), string(transition.source)), "/")
		}
		for index := range enter {
			current, ok = fsm.states[Path(path.Join(enter[:index+1]...))]
			if ok {
				fsm.enter(current, event)
			}
		}
		if target, ok = fsm.states[transition.target]; ok && target != nil {
			if target.kind == ChoiceKind {
				current = target
				for _, choice := range target.transitions {
					if !fsm.evaluate(choice.guard, event) {
						continue
					}

					transition = choice
					break
				}
				continue
			} else {
				return fsm.initial(target, event)
			}
		}
		return nil
	}
	return nil
}

func (fsm *FSM) initial(state *state, event Event) *state {
	if fsm == nil {
		slog.Error("[process][initial] process is nil")
		return nil
	}
	initialPath := Path(".initial")
	if state != nil {
		initialPath = Path(path.Join(string(state.path), string(initialPath)))
	}
	initial, ok := fsm.model.states[initialPath]
	if !ok {
		if state == nil {
			slog.Warn("[process][initial] No initial state found", "path", initialPath)
		}
		return state
	}
	transition := initial.transitions[0]
	if transition == nil {
		slog.Warn("[process][initial] No initial transition found", "path", initialPath)
		return state
	}
	return fsm.transition(initial, transition, event)
}

func (fsm *FSM) wait(behavior *behavior) {
	if fsm == nil {
		return
	}
	active, ok := fsm.active[behavior]
	if !ok {
		return
	}
	<-active.channel
}

func (fsm *FSM) Dispatch(event Event) bool {
	if fsm == nil {
		slog.Error("[process][Dispatch] process is nil")
		return false
	}

	fsm.wait(fsm.model.behavior)
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()

	if fsm.state == nil {
		slog.Error("[process][Dispatch] Current state is nil")
		return false
	}

	states := strings.Split(string(fsm.state.path), "/")
	for index := range states {
		source, ok := fsm.model.states[Path(path.Join(states[:index+1]...))]
		if !ok {
			slog.Error("[process][Dispatch] Source state not found", "path", Path(path.Join(states[:index+1]...)))
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
			if !fsm.evaluate(transition.guard, event) {
				continue
			}
			fsm.state = fsm.transition(fsm.state, transition, event)
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
func On[E Dispatchable](events ...E) Element {
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
