package fsm

import (
	"context"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/gabewillen/fsm/kind"
)

const (
	initialPath Path = ".initial"
	AnyPath     Path = "*"
)

type builderContext struct {
	state      *state
	transition *transition
}

type ModelBuilder struct {
	*Model
	state      *state
	transition *transition
	stack      []builderContext
}

func (builder *ModelBuilder) path() Path {
	if builder.state == nil {
		return ""
	}
	return builder.state.path
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
	Kind         kind.Kind
	Event        Event
	CurrentState Path
	TargetState  Path
}

type active struct {
	channel chan struct{}
	cancel  context.CancelFunc
}

type fsm interface {
	Dispatch(event Event) bool
}

// FSM is a finite state machine.
type FSM struct {
	fsm
	*Model
	ctx       Context
	current   *state
	active    map[*behavior]*active
	mutex     *sync.Mutex
	listeners map[int]func(Trace)
}

var broadcastKey = Context{}

// New creates a new finite state machine having the specified initial state.
func New(ctx context.Context, model *Model) *FSM {

	fsm := &FSM{
		Model: &Model{
			behavior: &behavior{
				action: model.behavior.action,
			},
			states: model.states,
		},
	}
	return Init(ctx, fsm)
}

func Init(ctx context.Context, fsm *FSM) *FSM {
	machines, ok := ctx.Value(broadcastKey).(*sync.Map)
	if !ok {
		machines = &sync.Map{}
	}
	machines.Store(fsm, struct{}{})
	fsm.listeners = map[int]func(Trace){}
	fsm.active = map[*behavior]*active{}
	fsm.ctx = Context{Context: context.WithValue(ctx, broadcastKey, machines), FSM: fsm}
	fsm.mutex = &sync.Mutex{}
	fsm.execute(fsm.Model.behavior, nil)
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

func (fsm *FSM) evaluate(element *constraint, event Event) bool {
	if element == nil {
		return true
	}
	return element.expr(Context{FSM: fsm, Context: fsm.ctx}, event)
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

func (fsm *FSM) leave(state *state, event Event) {
	if state == nil {
		return
	}
	slog.Debug("[state][leave] leave", "state", state.path, "event", event)
	if state.submachine != nil {
		fsm.terminate(state.submachine.behavior)
	}
	fsm.terminate(state.activity)
	<-fsm.execute(state.exit, event)
}

func (fsm *FSM) enter(state *state, event Event) {
	slog.Debug("[state][enter] enter", "state", state, "event", event)
	if state == nil {
		return
	}
	<-fsm.execute(state.entry, event)
	fsm.execute(state.activity, event)
	if state.submachine != nil {
		submachine := Init(fsm.ctx, state.submachine)
		submachine.wait(submachine.behavior)
	}
}

func (fsm *FSM) execute(element *behavior, event Event) chan struct{} {
	channel := make(chan struct{})
	if fsm == nil || element == nil || element.action == nil {
		close(channel)
		return channel
	}
	current, ok := fsm.active[element]
	if current == nil || !ok {
		current = &active{}
		fsm.active[element] = current
	}
	var ctx context.Context
	ctx, current.cancel = context.WithCancel(fsm.ctx)
	current.channel = channel
	go func() {
		element.action(Context{FSM: fsm, Context: ctx}, event)
		close(current.channel)
	}()
	return current.channel
}

func (fsm *FSM) transition(current *state, transition *transition, event Event) *state {
	slog.Debug("[fsm][transition] transition", "transition", transition, "current", current, "event", event)
	var ok bool
	var target *state
	if fsm == nil {
		return nil
	}
	for transition != nil {
		fsm.notify(Trace{
			Kind:         kind.Transition,
			Event:        event,
			CurrentState: current.path,
			TargetState:  transition.target,
		})
		switch transition.kind {
		case kind.Internal:
			<-fsm.execute(transition.effect, event)
			return current
		case kind.Self, kind.External:
			exit := strings.Split(string(current.path), "/")
			for index := range exit {
				current, ok = fsm.states[Path(path.Join(exit[:len(exit)-index]...))]
				if ok {
					fsm.leave(current, event)
				}
			}
		}
		<-fsm.execute(transition.effect, event)
		var enter []string
		if transition.kind == kind.Self {
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
			if target.kind == kind.Choice {
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
		slog.Error("[FSM][initial] FSM is nil")
		return nil
	}
	initialPath := initialName
	if state != nil {
		initialPath = Path(path.Join(string(state.path), string(initialName)))
	}
	initial, ok := fsm.states[initialPath]
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

func Dispatch[T Dispatchable](fsm fsm, evt T) bool {
	if fsm == nil {
		slog.Error("[FSM][Dispatch] FSM is nil")
		return false
	}
	switch any(evt).(type) {
	case Event:
		return fsm.Dispatch(any(evt).(Event))
	case string:
		return fsm.Dispatch(NewEvent(any(evt).(string), nil))
	}
	return false
}

// true is returned if a transition has been applied, false otherwise.
func (fsm *FSM) Dispatch(event Event) bool {
	if fsm == nil {
		slog.Error("[FSM][Dispatch] FSM is nil")
		return false
	}

	fsm.wait(fsm.behavior)
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()

	if fsm.current == nil {
		slog.Error("[FSM][Dispatch] Current state is nil")
		return false
	}

	states := strings.Split(string(fsm.current.path), "/")
	for index := range states {
		source, ok := fsm.states[Path(path.Join(states[:index+1]...))]
		if !ok {
			slog.Error("[FSM][Dispatch] Source state not found", "path", Path(path.Join(states[:index+1]...)))
			return false
		}
		if source.submachine != nil && fsm.submachineState != source && source.submachine.Dispatch(event) {
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
			if !fsm.evaluate(transition.guard, event) {
				continue
			}
			fsm.current = fsm.transition(fsm.current, transition, event)
			return true
		}
	}
	return false
}

func (fsm *FSM) Context() *Context {
	return &fsm.ctx
}

func NewModel(elements ...Modelable) *Model {
	builder := &ModelBuilder{
		Model: &Model{
			behavior: &behavior{},
			states: map[Path]*state{
				initialName: {
					kind:        kind.Initial,
					path:        initialName,
					transitions: []*transition{},
				},
			},
		},
	}
	for _, buildable := range elements {
		buildable(builder)
	}
	builder.Model.behavior.action = func(ctx Context, event Event) {
		ctx.mutex.Lock()
		defer ctx.mutex.Unlock()
		ctx.current = ctx.initial(nil, event)
	}
	return builder.Model
}

// Reset resets the machine to its initial state.
func (f *FSM) Reset() {
	f.current = nil
}

// Current returns the current state.
func (fsm *FSM) State() *state {
	if fsm == nil {
		return nil
	}
	fsm.wait(fsm.behavior)
	return fsm.current
}
