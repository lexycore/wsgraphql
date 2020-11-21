package common

import (
	"errors"
	"net/http"

	"github.com/lexycore/wsgraphql/mutcontext"
	"github.com/lexycore/wsgraphql/proto"
)

const (
	// KeyHTTPRequest is a context key for http request
	KeyHTTPRequest = "wsgraphql_http_request"
)

var (
	// ErrSchemaRequired is a schema requirement error
	ErrSchemaRequired = errors.New("schema is required")

	// ErrPlainHTTPIgnored is an error issued when plain http request is ignored, for FuncPlainFail callback
	ErrPlainHTTPIgnored = errors.New("plain http request ignored")
)

// FuncConnectCallback is a prototype for OnConnect callback
type FuncConnectCallback func(globalCtx mutcontext.MutableContext, parameters interface{}) error

// FuncOperationCallback is a prototype for OnOperation callback
type FuncOperationCallback func(globalCtx, opctx mutcontext.MutableContext, operation *proto.PayloadOperation) error

// FuncOperationDoneCallback is a prototype for OnOperationDone callback
type FuncOperationDoneCallback func(globalCtx, opctx mutcontext.MutableContext, operation *proto.PayloadOperation) error

// FuncDisconnectCallback is a prototype for OnDisconnect callback
type FuncDisconnectCallback func(globalCtx mutcontext.MutableContext) error

// FuncPlainFail is a prototype for OnPlainFail callback
type FuncPlainFail func(globalCtx mutcontext.MutableContext, r *http.Request, w http.ResponseWriter, err error)

// FuncPlainInit is a prototype for OnPlainInit callback
type FuncPlainInit func(globalCtx mutcontext.MutableContext, r *http.Request, w http.ResponseWriter)
