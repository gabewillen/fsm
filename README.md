# fsm [![PkgGoDev](https://pkg.go.dev/badge/github.com/cocoonspace/fsm)](https://pkg.go.dev/github.com/cocoonspace/fsm) [![Build Status](https://app.travis-ci.com/cocoonspace/fsm.svg?branch=master)](https://app.travis-ci.com/cocoonspace/fsm) [![Coverage Status](https://coveralls.io/repos/github/cocoonspace/fsm/badge.svg?branch=master)](https://coveralls.io/github/cocoonspace/fsm?branch=master) [![Go Report Card](https://goreportcard.com/badge/github.com/cocoonspace/fsm)](https://goreportcard.com/report/github.com/cocoonspace/fsm)

Package fsm allows you to add [finite-state machines](https://en.wikipedia.org/wiki/Finite-state_machine) to your Go code.

States and events are defined as strings, and the FSM is created using a model-based approach:

```go
model := fsm.Model(
    fsm.Initial("foo"),
    fsm.State("foo"),
    fsm.State("bar"),
    fsm.Transition(
        fsm.On("foo"),
        fsm.Source("foo"),
        fsm.Target("bar"),
    ),
)

f := fsm.New(context.Background(), model)
```

You can add guards (conditions) and effects (actions) to transitions:

```go
fsm.Transition(
    fsm.On("foo"),
    fsm.Source("foo"),
    fsm.Target("bar"),
    fsm.Guard(func(ctx fsm.Context, event fsm.Event, data any) bool {
        // check something
        return true
    }),
    fsm.Effect(func(ctx fsm.Context, event fsm.Event, data any) {
        // do something
    }),
)
```

States can have entry, activity, and exit actions:

```go
fsm.State("foo",
    fsm.Entry(func(ctx fsm.Context, event fsm.Event, data any) {
        // called when entering state
    }),
    fsm.Activity(func(ctx fsm.Context, event fsm.Event, data any) {
        // long-running activity while in state
        <-ctx.Done() // will be cancelled on state exit
    }),
    fsm.Exit(func(ctx fsm.Context, event fsm.Event, data any) {
        // called when leaving state
    }),
)
```

You can create hierarchical state machines using submachines:

```go
subMachine := fsm.Model(
    fsm.Initial("sub1"),
    fsm.State("sub1"),
    // ... submachine states and transitions
)

mainModel := fsm.Model(
    fsm.Initial("main1"),
    fsm.State("main1", fsm.Submachine(subMachine)),
    // ... main machine states and transitions
)
```

You can listen to state transitions:

```go
f.AddListener(func(trace fsm.Trace) {
    // trace contains information about the transition:
    // - Kind: "transition" or "dispatch"
    // - Event: the event that triggered the transition
    // - CurrentState: the state before transition
    // - TargetState: the state after transition
    // - Data: any associated data
})
```

## Installation

```
go get github.com/cocoonspace/fsm
```

## Contribution guidelines

Contributions are welcome, as long as:

* unit tests & comments are included,
* no external package is used.

## License

MIT - See LICENSE
