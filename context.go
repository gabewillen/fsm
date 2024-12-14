package fsm

import (
	"context"
	"log/slog"
	"sync"
)

type Context struct {
	*FSM
	context.Context
}

func (ctx *Context) Broadcast(event Event) {
	machines, ok := ctx.Value(broadcastKey).(*sync.Map)
	if !ok {
		slog.Warn("[fsm][Broadcast] no machines found in context")
		return
	}
	machines.Range(func(value any, _ any) bool {
		go value.(*FSM).Dispatch(event)
		return true
	})
}
