// Package wsgraphql is a graphql over websocket transport using apollo websocket protocol
//
// Usage
//
// When subscription operation is present in query, it is called repeatedly until it would return an error or cancel
// context associated with this operation.
//
// Context provided to operation is also an instance of mutcontext.MutableContext, which supports setting additional
// values on same instance and holds cancel() function within it.
//
// Implementors of subscribable operation are expected to cast provided context to mutcontext.MutableContext and on
// first invocation initialize data persisted across calls to it (like, external connection, database cursor or
// anything like that) as well as cleanup function using mutcontext.MutableContext.SetCleanup(func()), which would be
// called once operation is complete.
//
// After initialization, subscription would be called repeatedly, expected to return next value at each invocation,
// until an error is encountered, interruption is requested, or data is normally exhausted, in which case
// mutcontext.MutableContext is supposed to be issued with .Complete()
//
// After any of that, cleanup function (if any provided) would be called, ensuring operation code could release any
// resources allocated
//
// Non-subscribable operations are straight-forward stateless invocations.
//
// Subscriptions are also not required to be stateful, however that would mean they would return values as fast as
// they could produce them, without any timeouts and delays implemented, which would likely to require some state,
// e.g. time of last invocation.
//
// By default, implementation allows calling any operation once via non-websocket plain http request.
package wsgraphql

import (
	"net/http"
	"reflect"
	"time"

	"github.com/gorilla/websocket"
	"github.com/graphql-go/graphql"

	"github.com/lexycore/wsgraphql/common"
	"github.com/lexycore/wsgraphql/mutcontext"
	"github.com/lexycore/wsgraphql/server"
)

const (
	// KeyHTTPRequest is a context key for http request
	KeyHTTPRequest = common.KeyHTTPRequest
)

var (
	// ErrSchemaRequired is a schema requirement error
	ErrSchemaRequired = common.ErrSchemaRequired
	// ErrPlainHTTPIgnored is an error issued when plain http request is ignored, for FuncPlainFail callback
	ErrPlainHTTPIgnored = common.ErrPlainHTTPIgnored
)

// FuncConnectCallback is a prototype for OnConnect callback
type FuncConnectCallback common.FuncConnectCallback

// FuncOperationCallback is a prototype for OnOperation callback
type FuncOperationCallback common.FuncOperationCallback

// FuncOperationDoneCallback is a prototype for OnOperationDone callback
type FuncOperationDoneCallback common.FuncOperationDoneCallback

// FuncDisconnectCallback is a prototype for OnDisconnect callback
type FuncDisconnectCallback common.FuncDisconnectCallback

// FuncPlainInit is a prototype for OnPlainFail callback
type FuncPlainInit common.FuncPlainInit

// FuncPlainFail is a prototype for OnPlainInit callback
type FuncPlainFail common.FuncPlainFail

// Config for websocket graphql server
type Config struct {
	// Upgrader is a websocket upgrader
	// default: one that simply negotiates 'graphql-ws' protocol
	Upgrader *websocket.Upgrader

	// Schema is a graphql schema, required
	// default: nil
	Schema *graphql.Schema

	// OnConnect called when new client is connecting with new parameters or new plain request started
	// default: nothing
	OnConnect FuncConnectCallback

	// OnOperation called when new operation started
	// default: nothing
	OnOperation FuncOperationCallback

	// OnOperationDone called when operation is complete
	// default: nothing
	OnOperationDone FuncOperationDoneCallback

	// OnDisconnect called when websocket connection is closed or plain request is served
	// default: nothing
	OnDisconnect FuncDisconnectCallback

	// OnPlainInit called when new http connection is established
	// default: nothing
	OnPlainInit FuncPlainInit

	// OnPlainFail called when failure occurred at plain http stages
	// default: writes back error text
	OnPlainFail FuncPlainFail

	// IgnorePlainHTTP if true, plain http connections that can't be upgraded would be ignored and not served as one-off requests
	// default: false
	IgnorePlainHTTP bool

	// KeepAlive is a keep alive period, at which server would send keep-alive messages
	// default: 20 seconds
	KeepAlive time.Duration
}

func setDefault(value, def interface{}) {
	v := reflect.ValueOf(value).Elem()
	needDef := false
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Ptr, reflect.Interface, reflect.Slice:
		needDef = v.IsNil()
	default:
		zer := reflect.Zero(v.Type())
		needDef = v.Interface() == zer.Interface()
	}

	if needDef {
		v.Set(reflect.ValueOf(def))
	}
}

// NewServer returns new server instance using supplied config (which could be zero-value)
func NewServer(config Config) (http.Handler, error) {
	if config.Schema == nil {
		return nil, ErrSchemaRequired
	}
	setDefault(&config.Upgrader, &websocket.Upgrader{Subprotocols: []string{"graphql-ws"}})
	setDefault(&config.OnPlainFail, func(globalctx mutcontext.MutableContext, r *http.Request, w http.ResponseWriter, err error) {
		_, _ = w.Write([]byte(err.Error()))
	})
	setDefault(&config.KeepAlive, time.Second*20)

	return &server.Server{
		Upgrader:        config.Upgrader,
		Schema:          config.Schema,
		OnConnect:       common.FuncConnectCallback(config.OnConnect),
		OnOperation:     common.FuncOperationCallback(config.OnOperation),
		OnOperationDone: common.FuncOperationDoneCallback(config.OnOperationDone),
		OnDisconnect:    common.FuncDisconnectCallback(config.OnDisconnect),
		OnPlainInit:     common.FuncPlainInit(config.OnPlainInit),
		OnPlainFail:     common.FuncPlainFail(config.OnPlainFail),
		IgnorePlainHTTP: config.IgnorePlainHTTP,
		KeepAlive:       config.KeepAlive,
	}, nil
}
