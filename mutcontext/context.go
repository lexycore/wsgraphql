// Package mutcontext implements mutable context allowing easy adding values shared across all context and it's children
package mutcontext

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNoCancel returned if no cancel func present
var ErrNoCancel = errors.New("no cancel func")

// FuncCleanup is a cleanup closure
type FuncCleanup func()

// MutableContext interface, allows easy setting values and keeping cancel() function
type MutableContext interface {
	context.Context
	Set(key, value interface{})
	Cancel() error
	SetCleanup(cleanup FuncCleanup)
	Complete()
	Completed() bool
}

// Basic interface implementation, also uses map instead of delegates for efficiency for many keys
type mutableContext struct {
	Context     context.Context
	CancelFunc  context.CancelFunc
	CleanupFunc FuncCleanup
	Values      map[interface{}]interface{}
	IsComplete  bool
	mutex       *sync.Mutex
}

// Deadline is a pass-through to parent context
func (ctx *mutableContext) Deadline() (deadline time.Time, ok bool) {
	return ctx.Context.Deadline()
}

// Done is a pass-through to parent context
func (ctx *mutableContext) Done() <-chan struct{} {
	return ctx.Context.Done()
}

// Err is a pass-through to parent context
func (ctx *mutableContext) Err() error {
	return ctx.Context.Err()
}

// Value is a pass-through to parent context if value is not in a local map. If exists - use local
func (ctx *mutableContext) Value(key interface{}) interface{} {
	ctx.mutex.Lock()
	defer ctx.mutex.Unlock()
	if v, ok := ctx.Values[key]; ok {
		return v
	}
	return ctx.Context.Value(key)
}

// Set value in local map
func (ctx *mutableContext) Set(key, value interface{}) {
	ctx.mutex.Lock()
	defer ctx.mutex.Unlock()
	ctx.Values[key] = value
}

// Cancel calls cancel() function if there is one, otherwise returns an error
func (ctx *mutableContext) Cancel() error {
	if ctx.IsComplete {
		return ctx.Err()
	}
	ctx.Complete()
	if ctx.CancelFunc == nil {
		return ErrNoCancel
	}
	ctx.CancelFunc()
	return nil
}

// SetCleanup is an assigned cleanup function
func (ctx *mutableContext) SetCleanup(cleanup FuncCleanup) {
	ctx.CleanupFunc = cleanup
}

// Complete context gracefully
func (ctx *mutableContext) Complete() {
	if ctx.IsComplete {
		return
	}
	ctx.IsComplete = true
	_ = ctx.Cancel()
	if ctx.CleanupFunc != nil {
		ctx.CleanupFunc()
	}
}

// Completed indicates context was completed normally
func (ctx *mutableContext) Completed() bool {
	return ctx.IsComplete
}

// CreateNew is a constructor without cancel() function, will make ctx.Cancel() != nil
func CreateNew(ctx context.Context) MutableContext {
	return &mutableContext{
		Context: ctx,
		Values:  make(map[interface{}]interface{}),
		mutex:   &sync.Mutex{},
	}
}

// CreateNewCancel is a constructor with cancel() function
func CreateNewCancel(ctx context.Context, cancel context.CancelFunc) MutableContext {
	return &mutableContext{
		Context:    ctx,
		CancelFunc: cancel,
		Values:     make(map[interface{}]interface{}),
		mutex:      &sync.Mutex{},
	}
}
