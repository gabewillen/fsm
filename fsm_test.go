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
	// slog.SetLogLoggerLevel(slog.LevelDebug)

	model := fsm.New(
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
	f := fsm.Execute(context.Background(), model)
	if f.State().Path() != "foo" {
		t.Error("Initial state is not foo", "state", f.State())
		return
	}
	ok := f.Send(fsm.NewEvent("foo", nil))
	if !ok {
		t.Error("Event returned false")
	}
	if f.State().Path() != "bar" {
		t.Error("Bad target state")
	}
	fsm.Terminate(&f)
	if f != nil {
		t.Fatal("Bad state after terminate", f)
	}
}

// type Foo struct {
// 	*fsm.FSM
// }

func TestGuard(t *testing.T) {
	check := false
	model := fsm.New(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
			fsm.Guard(func(ctx fsm.Context, event fsm.Event) bool {
				return check
			}),
		),
	)
	f := fsm.Execute(context.Background(), model)
	ok := f.Send(fsm.NewEvent("foo", nil))
	if ok || f.State().Path() == "bar" {
		t.Error("Transition should not happen because of Check")
	}
	check = true
	ok = f.Send(fsm.NewEvent("foo", nil))
	if !ok || f.State().Path() != "bar" {
		t.Error("Transition should happen thanks to Check")
	}
}

func TestChoice(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	check := false
	model := fsm.New(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.State("bar"),
		fsm.State("baz"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Choice(
				fsm.Transition(
					fsm.Target("bar"),
					fsm.Guard(func(ctx fsm.Context, event fsm.Event) bool {
						return check
					}),
				),
				fsm.Transition(
					fsm.Target("baz"),
					fsm.Guard(func(ctx fsm.Context, event fsm.Event) bool {
						return !check
					}),
				),
			),
		),
	)
	f := fsm.Execute(context.Background(), model)
	slog.Info("f", "f", f)
	ok := f.Send(fsm.NewEvent("foo", nil))
	if !ok || f.State().Path() != "baz" {
		t.Error("Should transition to baz when check is false", "state", f.State().Path())
	}
	check = true
	f = fsm.Execute(context.Background(), model)
	ok = f.Send(fsm.NewEvent("foo", nil))
	if !ok || f.State().Path() != "bar" {
		t.Error("Should transition to bar when check is true")
	}
}

func TestEffect(t *testing.T) {
	call := false
	model := fsm.New(
		fsm.Initial("foo"),
		fsm.State("foo"),
		fsm.State("bar"),
		fsm.Transition(
			fsm.On("foo"),
			fsm.Source("foo"),
			fsm.Target("bar"),
			fsm.Effect(func(ctx fsm.Context, event fsm.Event) {
				call = true
			}),
		),
	)
	f := fsm.Execute(context.Background(), model)
	ok := f.Send(fsm.NewEvent("foo", nil))
	if !ok {
		t.Error("Event returned false")
	}
	if !call {
		t.Error("Call should have been called")
	}
}

// func TestOnTransition(t *testing.T) {
// 	model := fsm.NewModel(
// 		fsm.Initial("foo"),
// 		fsm.State("foo"),
// 		fsm.Transition(
// 			fsm.On("foo"),
// 			fsm.Source("foo"),
// 			fsm.Target("bar"),
// 		),
// 		fsm.Transition(
// 			fsm.On("bar"),
// 			fsm.Source("bar"),
// 			fsm.Target("foo"),
// 		),
// 	)
// 	f := fsm.New(context.Background(), model)
// 	var calls int
// 	f.AddListener(func(trace fsm.Trace) {
// 		calls++
// 	})
// 	_, _ = f.Dispatch("foo", nil)
// 	if calls != 2 {
// 		t.Error("OnTransition func has not been called", "calls", calls)
// 		return
// 	}
// 	_, _ = f.Dispatch("bar", nil)
// 	if calls != 3 {
// 		t.Error("OnTransition func has not been called", "calls", calls)
// 		return
// 	}
// }

func TestActivityTermination(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	var wg sync.WaitGroup
	wg.Add(1)
	activityRunning := false

	model := fsm.New(
		fsm.Initial("foo",
			fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
				t.Log("Entry action started")
			}),
			fsm.Activity(func(ctx fsm.Context, event fsm.Event) {
				t.Log("Activity started")
				activityRunning = true
				wg.Done()
				<-ctx.Done() // Block until context cancelled
				activityRunning = false
			}),
		),
		fsm.State("bar",
			fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
				t.Log("Entry action started")
			}),
		),
		fsm.Transition(
			fsm.On("next"),
			fsm.Source("foo"),
			fsm.Target("bar"),
		),
	)
	f := fsm.Execute(context.Background(), model)
	// Wait for activity to start
	wg.Wait()
	t.Log("Activity started")
	if !activityRunning {
		t.Error("Activity should be running")
	}

	// Transition should terminate activity
	f.Send(fsm.NewEvent("next", nil))
	t.Log("Transition dispatched")
	// Give activity goroutine time to clean up

	if activityRunning {
		t.Error("Activity should have been terminated")
	}
}

func TestSubmachine(t *testing.T) {
	a := fsm.New(
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
	model := fsm.New(
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
	f := fsm.Execute(context.Background(), model)
	submachine := f.State().Submachine()
	if submachine == nil {
		t.Error("Submachine is nil")
		return
	}
	slog.Info("Submachine", "submachine", submachine)

	if submachine.State().Path() != "foo" {
		slog.Error("bad submachine state", "state", submachine.State())
		t.Error("bad submachine state", submachine.State().Path(), "expected", "foo")
		return
	}

	f.Send(fsm.NewEvent("foo", nil))
	t.Log("submachine", f.State().Submachine().State().Path())
	if f.State().Submachine().State().Path() != "bar" {
		t.Error("Bad state")
		return
	}
	f.Send(fsm.NewEvent("a", nil))
	if f.State().Path() != "b" {
		t.Error("failed to transition from a to b")
		return
	}

}

func TestNestedStates(t *testing.T) {
	actions := []string{}
	testState := func(name string, states ...fsm.Element) fsm.Element {
		entry := fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
			actions = append(actions, name+"/entry")
		})
		exit := fsm.Exit(func(ctx fsm.Context, event fsm.Event) {
			actions = append(actions, name+"/exit")
		})
		return fsm.State(name, append(states, entry, exit)...)
	}
	model := fsm.New(
		fsm.Initial("a/b/c"),
		testState("a", testState("b", testState("c"))),
		testState("bar"),
		fsm.Transition(
			fsm.On("a"),
			fsm.Source("a"),
			fsm.Target("bar"),
		),
	)
	f := fsm.Execute(context.Background(), model)
	if f.State().Path() != "a/b/c" {
		t.Error("fsm state is not initial state a/b/c")
		return
	}
	if len(actions) != 3 {
		t.Error("Actions not called", "actions", actions)
		return
	}
	if actions[0] != "a/entry" {
		t.Error("a/entry not called")
	}
	if actions[1] != "b/entry" {
		t.Error("b/entry not called")
	}
	if actions[2] != "c/entry" {
		t.Error("c/entry not called")
	}
	actions = []string{}
	ok := f.Send(fsm.NewEvent("a", nil))
	if !ok {
		t.Error("a not called")
	}
	if f.State().Path() != "bar" {
		t.Error("fsm state is not bar")
	}
	if len(actions) != 4 {
		t.Error("Actions not called", "actions", actions)
		return
	}
	if actions[0] != "c/exit" {
		t.Error("a/exit not called")
	}
	if actions[1] != "b/exit" {
		t.Error("b/exit not called")
	}
	if actions[2] != "a/exit" {
		t.Error("a/exit not called")
	}
	if actions[3] != "bar/entry" {
		t.Error("bar/entry not called")
	}
}

func TestNestedInitial(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	model := fsm.New(
		fsm.Initial("a"),
		fsm.State("a", fsm.Initial("b"), fsm.State("b")),
	)
	f := fsm.Execute(context.Background(), model)
	if f.State().Path() != "a/b" {
		t.Error("fsm state is not initial state a/b", "state", f.State().Path())
		return
	}
}

func TestNestedTransitions(t *testing.T) {
	var entryCalls int
	model := fsm.New(
		fsm.Initial("a"),
		fsm.State("a",
			fsm.Initial("b"),
			fsm.State("b"),
			fsm.State("c"),
			fsm.Transition(
				fsm.On("a"),
				fsm.Source("b"),
				fsm.Target("c"),
			),
		),
		fsm.State("b",
			fsm.Initial("c", fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
				entryCalls++
			})),
			fsm.Transition(
				fsm.On("b"),
				fsm.Source("../a"),
				fsm.Target("../b"),
			),
		),
	)
	f := fsm.Execute(context.Background(), model)
	if f.State().Path() != "a/b" {
		t.Fatal("fsm state is not initial state a/b", "state", f.State().Path())
	}
	f.Send(fsm.NewEvent("a", nil))
	if f.State().Path() != "a/c" {
		t.Fatal("fsm state is not a/c", "state", f.State().Path())
	}
	f.Send(fsm.NewEvent("b", nil))
	if f.State().Path() != "b/c" {
		t.Fatal("fsm state is not b", "state", f.State().Path())
	}
	if entryCalls != 1 {
		t.Fatal("entryCalls not called", "entryCalls", entryCalls)
	}
}

func TestBroadcast(t *testing.T) {
	a := fsm.New(
		fsm.Initial("a"),
		fsm.State("a"),
	)
	b := fsm.New(
		fsm.Initial("b"),
		fsm.State("b"),
	)
	c := fsm.New(
		fsm.Initial("c"),
		fsm.State("c"),
	)
	aFSM := fsm.Execute(context.Background(), a)
	bFSM := fsm.Execute(aFSM, b)
	cFSM := fsm.Execute(bFSM, c)
	aFSM.Send(fsm.NewEvent("a", nil))
	bFSM.Send(fsm.NewEvent("b", nil))
	cFSM.Broadcast(fsm.NewEvent("c", nil))
}

func TestSelfTransition(t *testing.T) {
	entry, exit, activity := 0, 0, 0

	model := fsm.New(
		fsm.Initial("a",
			fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
				entry++
			}),
			fsm.Activity(func(ctx fsm.Context, event fsm.Event) {
				activity++
			}),
			fsm.Exit(func(ctx fsm.Context, event fsm.Event) {
				exit++
			}),
		),
		fsm.Transition(
			fsm.On("a"),
			fsm.Source("a"),
			fsm.Target("a"),
		),
	)
	f := fsm.Execute(context.Background(), model)
	f.State()
	if entry != 1 {
		t.Fatal("Entry action not called")
	}
	time.Sleep(1 * time.Millisecond)
	if activity != 1 {
		t.Fatal("Activity action not called")
	}
	f.Send(fsm.NewEvent("a", nil))
	if exit != 1 {
		t.Fatal("Exit action not called")
	}
	if entry != 2 {
		t.Fatal("Entry action not called", "entry", entry)
	}
	time.Sleep(1 * time.Millisecond)
	if activity != 2 {
		t.Fatal("Activity action not called", "activity", activity)
	}
}

func TestInitialWithChoice(t *testing.T) {
	model := fsm.New(
		fsm.Initial(fsm.Choice(fsm.Transition(fsm.Target("b")), fsm.Transition(fsm.Target("c")))),
		fsm.State("a"),
		fsm.State("b"),
		fsm.State("c"),
	)
	f := fsm.Execute(context.Background(), model)
	if f.State().Path() != "b" {
		t.Fatal("fsm state is not b", "state", f.State().Path())
	}
}

func TestInternalTransition(t *testing.T) {
	effectCalled := false
	model := fsm.New(
		fsm.Initial("a"),
		fsm.State("a"),
		fsm.Transition(
			fsm.On("a"),
			fsm.Source("a"),
			fsm.Effect(func(ctx fsm.Context, event fsm.Event) {
				effectCalled = true
			}),
		),
	)
	f := fsm.Execute(context.Background(), model)
	f.Send(fsm.NewEvent("a", nil))
	if !effectCalled {
		t.Fatal("Effect not called")
	}
}

func TestTransitionFromNestedEntry(t *testing.T) {
	var entryCalls int
	model := fsm.New(
		fsm.Initial("a"),
		fsm.State("a",
			fsm.Initial("b"),
			fsm.State("b", fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
				go ctx.Send(fsm.NewEvent("a", nil))
			})),
			fsm.State("c"),
			fsm.Transition(
				fsm.On("a"),
				fsm.Source("b"),
				fsm.Target("c"),
			),
		),
		fsm.State("b",
			fsm.Initial("c", fsm.Entry(func(ctx fsm.Context, event fsm.Event) {
				entryCalls++
			})),
			fsm.Transition(
				fsm.On("b"),
				fsm.Source("../a"),
				fsm.Target("../b"),
			),
		),
	)
	f := fsm.Execute(context.Background(), model)
	if f.State().Path() != "a/b" {
		t.Fatal("fsm state is not initial state a/b", "state", f.State().Path())
	}
	f.Send(fsm.NewEvent("a", nil))
	if f.State().Path() != "a/c" {
		t.Fatal("fsm state is not a/c", "state", f.State().Path())
	}
	f.Send(fsm.NewEvent("b", nil))
	if f.State().Path() != "b/c" {
		t.Fatal("fsm state is not b", "state", f.State().Path())
	}
	if entryCalls != 1 {
		t.Fatal("entryCalls not called", "entryCalls", entryCalls)
	}
}

func TestLocalTransition(t *testing.T) {
	model := fsm.New(
		fsm.Initial("a"),
		fsm.State("a", fsm.State("b")),
		fsm.Transition(
			fsm.On("a"),
			fsm.Source("a"),
			fsm.Target("a/b"),
		),
	)
	f := fsm.Execute(context.Background(), model)
	f.Send(fsm.NewEvent("a", nil))
	if f.State().Path() != "a/b" {
		t.Fatal("fsm state is not a/b", "state", f.State().Path())
	}
}

func TestCompletionEvent(t *testing.T) {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	model := fsm.New(
		fsm.Initial("a"),
		fsm.State("a", fsm.Transition(fsm.Target("../b"))),
		fsm.State("b"),
	)
	f := fsm.Execute(context.Background(), model)
	f.Send(fsm.NewEvent("a", nil))
	if f.State().Path() == "a" {
		t.Fatal("fsm state is not b", "state", f.State().Path())
	}

	// f.Send(fsm.NewEvent("a", nil))
	// if f.State().Path() != "b" {
	// 	t.Fatal("fsm state is not b", "state", f.State().Path())
	// }
}
