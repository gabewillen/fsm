package fsm_test

import (
	"fmt"
	"testing"

	"github.com/gabewillen/fsm"
)

const (
	StateFoo fsm.State = iota
	StateBar
)

const (
	EventFoo fsm.Event = iota
	EventBar
)

func TestFSM(t *testing.T) {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo),
		fsm.Dst(StateBar),
	)
	res := f.Dispatch(EventFoo, nil)
	if !res {
		t.Error("Event returned false")
	}
	if f.Current() != StateBar {
		t.Error("Bad destination state")
	}
	f.Reset()
	if f.Current() != StateFoo {
		t.Error("Bad state after Reset")
	}
}

func TestCheck(t *testing.T) {
	check := false
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo), fsm.Check(func() bool {
			return check
		}),
		fsm.Dst(StateBar),
	)
	res := f.Dispatch(EventFoo, nil)
	if res || f.Current() == StateBar {
		t.Error("Transition should not happen because of Check")
	}
	check = true
	res = f.Dispatch(EventFoo, nil)
	if !res && f.Current() != StateBar {
		t.Error("Transition should happen thanks to Check")
	}
}

func ExampleCheck() {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo), fsm.Check(func() bool {
			return true
		}),
		fsm.Dst(StateBar),
	)
}

func TestNotCheck(t *testing.T) {
	check := true
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo), fsm.NotCheck(func() bool {
			return check
		}),
		fsm.Dst(StateBar),
	)
	res := f.Dispatch(EventFoo, nil)
	if res || f.Current() == StateBar {
		t.Error("Transition should not happen because of NotCheck")
	}
	check = false
	res = f.Dispatch(EventFoo, nil)
	if !res && f.Current() != StateBar {
		t.Error("Transition should happen thanks to NotCheck")
	}
}

func TestCall(t *testing.T) {
	call := false
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo),
		fsm.Call(func() {
			call = true
		}),
	)
	_ = f.Dispatch(EventFoo, nil)
	if !call {
		t.Error("Call should have been called")
	}
}

func ExampleCall() {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo),
		fsm.Dst(StateBar), fsm.Call(func() {
			fmt.Println("Call called")
		}),
	)
}

func TestTimes(t *testing.T) {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo), fsm.Times(2),
		fsm.Dst(StateBar),
	)
	f.Transition(
		fsm.On(EventBar), fsm.Src(StateBar),
		fsm.Dst(StateFoo),
	)

	res := f.Dispatch(EventFoo, nil)
	if res || f.Current() == StateBar {
		t.Error("Transition should not happen the first time")
	}
	res = f.Dispatch(EventFoo, nil)
	if !res || f.Current() != StateBar {
		t.Error("Transition should happen the second time")
	}
	res = f.Dispatch(EventBar, nil)
	if !res || f.Current() != StateFoo {
		t.Error("FSM should have returned to StateFoo")
	}
	res = f.Dispatch(EventFoo, nil)
	if res || f.Current() == StateBar {
		t.Error("Transition should not happen the first time of the second run")
	}
	res = f.Dispatch(EventFoo, nil)
	if !res || f.Current() != StateBar {
		t.Error("Transition should happen the second time of the second run")
	}
}

func ExampleTimes() {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo), fsm.Times(2),
		fsm.Dst(StateBar),
	)

	_ = f.Dispatch(EventFoo, nil) // no transition
	_ = f.Dispatch(EventFoo, nil) // transition to StateBar
}

func TestEnterExit(t *testing.T) {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo),
		fsm.Dst(StateBar),
	)
	f.Transition(
		fsm.On(EventBar), fsm.Src(StateBar),
		fsm.Dst(StateFoo),
	)
	var entry, exit fsm.State
	f.Enter(func(state fsm.State) {
		entry = state
	})
	f.Exit(func(state fsm.State) {
		exit = state
	})

	_ = f.Dispatch(EventFoo, nil)
	if entry != StateBar {
		t.Error("Enter func has not been called")
	}
	if exit != StateFoo {
		t.Error("Exit func has not been called")
	}
	_ = f.Dispatch(EventBar, nil)
	if entry != StateFoo {
		t.Error("Enter func has not been called")
	}
	if exit != StateBar {
		t.Error("Exit func has not been called")
	}
}

func TestEnterExitState(t *testing.T) {
	f := fsm.New(StateFoo)
	f.Transition(
		fsm.On(EventFoo), fsm.Src(StateFoo),
		fsm.Dst(StateBar),
	)
	f.Transition(
		fsm.On(EventBar), fsm.Src(StateBar),
		fsm.Dst(StateFoo),
	)
	entry, exit := false, false
	f.EnterState(StateBar, func() {
		entry = true
	})
	f.ExitState(StateBar, func() {
		exit = true
	})

	_ = f.Dispatch(EventFoo, nil)
	if !entry {
		t.Error("EnterState func has not been called")
	}
	if exit {
		t.Error("ExitState func has wrongly been called")
	}
	entry, exit = false, false
	_ = f.Dispatch(EventBar, nil)
	if entry {
		t.Error("EnterState func has wrongly been called")
	}
	if !exit {
		t.Error("ExitState func has not been called")
	}
}
