package fsm

import (
	"fmt"
	"log/slog"
	"path"

	"github.com/gabewillen/fsm/kind"
)

type Path string

// type Event string
type Action func(Context, Event)
type Constraint func(Context, Event) bool

type Modelable func(*ModelBuilder)

type element interface{}

type constraint struct {
	expr Constraint
}

type behavior struct {
	action Action
}

type state struct {
	path        Path
	kind        kind.Kind
	entry       *behavior
	activity    *behavior
	exit        *behavior
	transitions []*transition
	submachine  *FSM
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
	kind   kind.Kind
	guard  *constraint
	effect *behavior
	target Path
	source Path
}

type Model struct {
	*behavior
	states          map[Path]*state
	submachineState *state
}

type Event interface {
	element
	Kind() string
	Data() any
}

type event struct {
	kind string
	data any
}

func (e *event) Kind() string {
	return e.kind
}

func (e *event) Data() any {
	return e.data
}

func NewEvent(kind string, data any) Event {
	return &event{kind: kind, data: data}
}

var initialName = Path(".initial")

func Initial[T Targetable](name T, partialElements ...Modelable) Modelable {
	return func(model *ModelBuilder) {
		currentPath := Path("")
		if model.state != nil {
			currentPath = model.state.path
		}
		initialPath := Path(path.Join(string(currentPath), string(initialName)))
		initial, ok := model.states[initialPath]
		if !ok {
			initial = &state{
				path:        initialPath,
				kind:        kind.Initial,
				transitions: []*transition{},
			}
			model.states[initialPath] = initial
		}
		initialTransition := &transition{
			events: []Event{},
			kind:   kind.External,
			source: initialPath,
		}
		var target *state
		switch any(name).(type) {
		case string, Path:
			id := asPath(any(name).(string))
			targetPath := Path(path.Join(string(currentPath), string(id)))
			target, ok = model.states[targetPath]
			if !ok {
				target = &state{
					path:        targetPath,
					kind:        kind.State,
					transitions: []*transition{},
				}
				model.states[targetPath] = target
			}
			initialTransition.target = targetPath

		case Modelable:
			partial := any(name).(Modelable)
			partialElements = append(partialElements, partial)
		}
		model.push(target, initialTransition)
		for _, partial := range partialElements {
			partial(model)
		}
		model.pop()
		initial.transitions = append(initial.transitions, initialTransition)
	}
}

func State(name string, partialElements ...Modelable) Modelable {
	statePath := asPath(name)
	return func(model *ModelBuilder) {
		if model.state != nil {
			statePath = Path(path.Join(string(model.state.path), string(statePath)))
		}
		stateElement, ok := model.states[statePath]
		if !ok {
			stateElement = &state{
				path:        statePath,
				kind:        kind.State,
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

func Entry(action Action) Modelable {
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

func Activity(fn Action) Modelable {
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

func Exit(action Action) Modelable {
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
func Source(sourcePath Path) Modelable {
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
				kind:        kind.State,
				transitions: []*transition{},
			}
			model.states[sourcePath] = source
		}
		source.transitions = append(source.transitions, model.transition)
		model.transition.source = sourcePath
	}
}

type Dispatchable interface {
	string | *event
}

// On defines the Event that triggers a Transition.
func On[E Dispatchable](events ...E) Modelable {
	return func(model *ModelBuilder) {
		if model.transition == nil {
			slog.Warn("[fsm][On] called outside of a transition, On can only be used inside of fsm.Transition(...)")
			return
		}
		for _, evt := range events {
			switch any(evt).(type) {
			case string:
				model.transition.events = append(model.transition.events, &event{kind: any(evt).(string)})
			case Event:
				model.transition.events = append(model.transition.events, any(evt).(Event))
			}
		}
	}
}

type Targetable interface {
	string | Modelable | Path
}

// Dst defines the new State the machine switches to after a Transition.
func Target[T Targetable](target T) Modelable {
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
					kind:        kind.State,
					transitions: []*transition{},
				}
			}
			model.transition.target = targetPath
		case Modelable:
			any(target).(Modelable)(model)
		}
	}
}

func Choice(transitions ...Modelable) Modelable {
	return func(model *ModelBuilder) {
		choicePath := Path("")
		if model.state != nil {
			choicePath = Path(path.Join(string(model.state.path), string(choicePath)))
		}

		choice := &state{
			path:        choicePath,
			kind:        kind.Choice,
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

func Guard(fn Constraint) Modelable {
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

func Submachine(submachine *Model) Modelable {
	return func(model *ModelBuilder) {
		if model.state == nil {
			slog.Warn("[fsm][Submachine] called outside of a state, Submachine can only be used inside of State(...)")
			return
		}

		submachine := &FSM{
			Model: &Model{
				behavior: &behavior{
					action: submachine.behavior.action,
				},
				states:          submachine.states,
				submachineState: model.state,
			},
			listeners: map[int]func(Trace){},
		}
		model.state.submachine = submachine
	}
}

func Effect(fn Action) Modelable {
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

func Transition(nodes ...Modelable) Modelable {
	return func(model *ModelBuilder) {
		transition := &transition{}
		model.push(model.state, transition)
		for _, node := range nodes {
			node(model)
		}
		model.pop()
		slog.Debug("[fsm][Transition] transition", "transition", transition)
		if model.state != nil && transition.source == "" {
			transition.source = model.state.path
			model.state.transitions = append(model.state.transitions, transition)
		} else if transition.target == "" {
			slog.Error("[fsm][Transition] target is empty", "transition", transition)
		}
		if transition.target == transition.source {
			transition.kind = kind.Self
		} else if transition.target == "" {
			transition.kind = kind.Internal
		} else if match, err := path.Match(string(transition.source)+"/*", string(transition.target)); err == nil && match {
			transition.kind = kind.Local
		} else {
			transition.kind = kind.External
		}
	}
}
