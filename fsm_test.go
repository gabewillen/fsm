package fsm_test

import (
	"context"
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
			fsm.Guard(func(event fsm.Event, data interface{}) bool {
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
			fsm.Effect(func(ctx context.Context, event fsm.Event, data interface{}) {
				call = true
			}),
		),
	)
	_ = f.Dispatch("foo", nil)
	if !call {
		t.Error("Call should have been called")
	}
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
	f.OnTransition(func(event fsm.Event, source, target string) {
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
