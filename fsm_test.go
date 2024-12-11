package fsm_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gabewillen/fsm"
)

func TestFSM(t *testing.T) {
	model := fsm.Model(
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
	f := fsm.New(context.Background(), model)
	time.Sleep(100 * time.Millisecond)
	if f.State().Name() != "foo" {
		t.Error("Initial state is not foo")
		return
	}
	res := f.Dispatch("foo", nil)
	if !res {
		t.Error("Event returned false")
	}
	if f.State().Name() != "bar" {
		t.Error("Bad target state")
	}
	f.Reset()
	if f.State().Name() != "" {
		t.Error("Bad state after Reset")
	}
}

type Foo struct {
	*fsm.FSM
}

func TestGuard(t *testing.T) {
	check := false
	model := fsm.Model(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
			fsm.Guard(func(ctx fsm.Context, event fsm.Event, data interface{}) bool {
				return check
			}),
		),
	)
	f := Foo{fsm.New(context.Background(), model)}
	time.Sleep(100 * time.Millisecond)
	res := f.Dispatch("foo", nil)
	if res || f.State().Name() == "bar" {
		t.Error("Transition should not happen because of Check")
	}
	check = true
	res = f.Dispatch("foo", nil)
	if !res && f.State().Name() != "bar" {
		t.Error("Transition should happen thanks to Check")
	}
}

// func TestChoice(t *testing.T) {
// 	check := false
// 	model := fsm.Model(
// 		fsm.Initial("foo"),
// 		fsm.State("foo"),
// 		fsm.State("bar"),
// 		fsm.State("baz"),
// 		fsm.Transition(
// 			fsm.On("foo"),
// 			fsm.Source("foo"),
// 			fsm.Choice(
// 				fsm.Transition(
// 					fsm.Target("bar"),
// 					fsm.Guard(func(ctx fsm.Context, event fsm.Event, data interface{}) bool {
// 						return check
// 					}),
// 				),
// 				fsm.Transition(
// 					fsm.Target("baz"),
// 					fsm.Guard(func(ctx fsm.Context, event fsm.Event, data interface{}) bool {
// 						return !check
// 					}),
// 				),
// 			),
// 		),
// 	)
// 	f := fsm.New(context.Background(), model)
// 	res := f.Dispatch("foo", nil)
// 	if !res || f.State().Name() != "baz" {
// 		t.Error("Should transition to baz when check is false")
// 	}

// 	check = true
// 	f.Reset()
// 	res = f.Dispatch("foo", nil)
// 	if !res || f.State().Name() != "bar" {
// 		t.Error("Should transition to bar when check is true")
// 	}
// }

func TestEffect(t *testing.T) {
	call := false
	model := fsm.Model(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.State("bar"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
			fsm.Effect(func(ctx fsm.Context, event fsm.Event, data interface{}) {
				call = true
			}),
		),
	)
	f := fsm.New(context.Background(), model)
	time.Sleep(100 * time.Millisecond)
	_ = f.Dispatch("foo", nil)
	if !call {
		t.Error("Call should have been called")
	}
}

func TestOnTransition(t *testing.T) {
	model := fsm.Model(
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
	f := fsm.New(context.Background(), model)
	var calls int
	f.AddListener(func(trace fsm.Trace) {
		calls++
	})
	_ = f.Dispatch("foo", nil)
	if calls != 2 {
		t.Error("OnTransition func has not been called", "calls", calls)
		return
	}
	_ = f.Dispatch("bar", nil)
	if calls != 3 {
		t.Error("OnTransition func has not been called", "calls", calls)
		return
	}
}

func TestActivityTermination(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	activityRunning := false

	model := fsm.Model(
		fsm.Initial("foo",
			fsm.Entry(func(ctx fsm.Context, event fsm.Event, data interface{}) {
				t.Log("Entry action started")
			}),
			fsm.Activity(func(ctx fsm.Context, event fsm.Event, data interface{}) {
				t.Log("Activity started")
				activityRunning = true
				wg.Done()
				<-ctx.Done() // Block until context cancelled
				activityRunning = false
			}),
		),
		fsm.State("bar",
			fsm.Entry(func(ctx fsm.Context, event fsm.Event, data interface{}) {
				t.Log("Entry action started")
			}),
		),
		fsm.Transition(
			fsm.On("next"),
			fsm.Source("foo"),
			fsm.Target("bar"),
		),
	)
	f := fsm.New(context.Background(), model)
	// Wait for activity to start
	wg.Wait()
	t.Log("Activity started")
	if !activityRunning {
		t.Error("Activity should be running")
	}

	// Transition should terminate activity
	f.Dispatch("next", nil)
	t.Log("Transition dispatched")
	// Give activity goroutine time to clean up
	time.Sleep(100 * time.Millisecond)

	if activityRunning {
		t.Error("Activity should have been terminated")
	}
}

func TestSubmachine(t *testing.T) {
	a := fsm.Model(
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
	model := fsm.Model(
		fsm.Initial("a"),
		fsm.State("a", fsm.Submachine(a)),
		fsm.State("b"),
		fsm.Transition(
			fsm.On("a"),
			fsm.Source("a"),
			fsm.Target("b"),
		),
	)

	slog.Info("Model", "model", model)
	f := fsm.New(context.Background(), model)
	time.Sleep(200 * time.Millisecond)
	submachine := f.State().Submachine()
	if submachine == nil {
		t.Error("Submachine is nil")
		return
	}
	slog.Info("Submachine", "submachine", submachine)
	submachine.AddListener(func(trace fsm.Trace) {
		t.Log("Submachine transition", trace)
	})
	time.Sleep(500 * time.Millisecond)
	if submachine.State().Name() != "foo" {
		slog.Error("bad submachine state", "state", submachine.State())
		t.Error("bad submachine state", submachine.State().Name(), "expected", "foo")
		return
	}

	f.Dispatch("foo", nil)
	t.Log("submachine", f.State().Submachine().State().Name())
	if f.State().Submachine().State().Name() != "bar" {
		t.Error("Bad state")
		return
	}
	f.Dispatch("a", nil)
	if f.State().Name() != "b" {
		t.Error("failed to transition from a to b")
		return
	}

}

// func TestNestedStates(t *testing.T) {
// 	model := fsm.Model(
// 		fsm.Initial("a/b/c"),
// 		fsm.State("a", fsm.State("b", fsm.State("c"))),
// 		fsm.State("bar"),
// 	)
// 	f := fsm.New(context.Background(), model)
// 	if f.State().Name() != "a/b/c" {
// 		t.Error("fsm state is not initial state a/b/c")
// 		return
// 	}
// }
