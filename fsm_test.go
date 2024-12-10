package fsm_test

import (
	"testing"

	"github.com/gabewillen/fsm"
)

func TestFSM(t *testing.T) {
	f := fsm.New(
		fsm.Initial("foo"),
		fsm.State(
			"foo",
		),
		fsm.State(
			"bar",
		),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
		),
	)
	if f.State() != "foo" {
		t.Error("Initial state is not foo")
		return
	}
	res := f.Dispatch("foo", nil)
	if !res {
		t.Error("Event returned false")
	}
	if f.State() != "bar" {
		t.Error("Bad target state")
	}
	f.Reset()
	if f.State() != "foo" {
		t.Error("Bad state after Reset")
	}
}

func TestGuard(t *testing.T) {
	check := false
	f := fsm.New(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
			fsm.Guard(func(fsm *fsm.FSM, event fsm.Event, data interface{}) bool {
				return check
			}),
		),
	)
	res := f.Dispatch("foo", nil)
	if res || f.State() == "bar" {
		t.Error("Transition should not happen because of Check")
	}
	check = true
	res = f.Dispatch("foo", nil)
	if !res && f.State() != "bar" {
		t.Error("Transition should happen thanks to Check")
	}
}

// func ExampleCheck() {
// 	f := fsm.New(StateFoo)
// 	f.Transition(
// 		fsm.On(EventFoo), fsm.Src(StateFoo), fsm.Check(func() bool {
// 			return true
// 		}),
// 		fsm.Dst(StateBar),
// 	)
// }

func TestEffect(t *testing.T) {
	call := false
	f := fsm.New(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.State("bar"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
			fsm.Effect(func(fsm *fsm.FSM, event fsm.Event, data interface{}) {
				call = true
			}),
		),
	)
	_ = f.Dispatch("foo", nil)
	if !call {
		t.Error("Call should have been called")
	}
}

type Foo struct {
	*fsm.FSM
}

func TestOnTransition(t *testing.T) {
	f := fsm.New(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
		),
		fsm.Transition(
			fsm.On("bar"),
			fsm.Source("bar"),
			fsm.Target("foo"),
		),
	)
	var calls int
	f.OnTransition(func(fsm *fsm.FSM, event fsm.Event, source, target string) {
		calls++
	})
	_ = f.Dispatch("foo", nil)
	if calls != 1 {
		t.Error("OnTransition func has not been called")
		return
	}
	_ = f.Dispatch("bar", nil)
	if calls != 2 {
		t.Error("OnTransition func has not been called")
		return
	}
}

// func TestEnterExitState(t *testing.T) {
// 	f := fsm.New(StateFoo)
// 	f.Transition(
// 		fsm.On(EventFoo), fsm.Src(StateFoo),
// 		fsm.Dst(StateBar),
// 	)
// 	f.Transition(
// 		fsm.On(EventBar), fsm.Src(StateBar),
// 		fsm.Dst(StateFoo),
// 	)
// 	entry, exit := false, false
// 	f.EnterState(StateBar, func() {
// 		entry = true
// 	})
// 	f.ExitState(StateBar, func() {
// 		exit = true
// 	})

// 	_ = f.Dispatch(EventFoo, nil)
// 	if !entry {
// 		t.Error("EnterState func has not been called")
// 	}
// 	if exit {
// 		t.Error("ExitState func has wrongly been called")
// 	}
// 	entry, exit = false, false
// 	_ = f.Dispatch(EventBar, nil)
// 	if entry {
// 		t.Error("EnterState func has wrongly been called")
// 	}
// 	if !exit {
// 		t.Error("ExitState func has not been called")
// 	}
// }
