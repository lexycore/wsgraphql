// Package server is a graphql websocket http handler implementation
package server

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/kinds"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"

	"github.com/lexycore/wsgraphql/common"
	"github.com/lexycore/wsgraphql/mutcontext"
	"github.com/lexycore/wsgraphql/proto"
)

// Server itself
type Server struct {
	Upgrader        *websocket.Upgrader
	Schema          *graphql.Schema
	OnConnect       common.FuncConnectCallback
	OnOperation     common.FuncOperationCallback
	OnOperationDone common.FuncOperationDoneCallback
	OnDisconnect    common.FuncDisconnectCallback
	OnPlainInit     common.FuncPlainInit
	OnPlainFail     common.FuncPlainFail
	IgnorePlainHTTP bool
	KeepAlive       time.Duration
}

// TickCloser is a ticker that also closes on stop
type TickCloser struct {
	Ticker  *time.Ticker
	Stopped chan bool
}

// Stop also closes
func (tc *TickCloser) Stop() {
	tc.Ticker.Stop()
	close(tc.Stopped)
}

// Event is an internal event base
type Event interface{}

// EventConnectionInit posted when Connection negotiation commences
type EventConnectionInit struct {
	Parameters interface{}
}

// EventConnectionTerminate posted when Connection termination requested
type EventConnectionTerminate struct {
}

// EventConnectionReadClosed posted when Connection read was closed
type EventConnectionReadClosed struct {
}

// EventConnectionWriteClosed posted when Connection write was closed
type EventConnectionWriteClosed struct {
}

// EventConnectionTimerClosed posted when Connection timer was closed
type EventConnectionTimerClosed struct {
}

// EventOperationStart posted when new operation requested
type EventOperationStart struct {
	ID      interface{}
	Payload *proto.PayloadOperation
}

// EventOperationStop posted when operation interruption requested
type EventOperationStop struct {
	ID interface{}
}

// EventOperationComplete posted when operation completed
type EventOperationComplete struct {
	ID interface{}
}

// Connection base state
type Connection struct {
	Server        *Server
	Subscriptions map[interface{}]*Subscription
	Context       mutcontext.MutableContext
	Outgoing      chan *proto.Message
	Events        chan Event
	TickCloser    *TickCloser
	StopCounter   int32
}

// Subscription is a base operation/Subscription state
type Subscription struct {
	ID      interface{}
	Payload *proto.PayloadOperation
	Context mutcontext.MutableContext
}

// ReadLoop reading bytes from websocket to messages, posting events
func (conn *Connection) ReadLoop(ws *websocket.Conn) {
	ws.SetReadLimit(-1)
	_ = ws.SetReadDeadline(time.Time{})
	payloadBytes := &proto.PayloadBytes{}
	template := proto.Message{
		Payload: payloadBytes,
	}
	for {
		payloadBytes.Value = nil
		payloadBytes.Bytes = nil
		msg := template

		err := ws.ReadJSON(&msg)
		if err != nil {
			_ = ws.Close()
			conn.Events <- &EventConnectionReadClosed{}
			break
		}
		switch msg.Type {
		case proto.GQLConnectionInit:
			var value interface{}
			if len(payloadBytes.Bytes) > 0 {
				err := json.Unmarshal(payloadBytes.Bytes, &value)
				if err != nil {
					conn.Outgoing <- proto.NewMessage("", err.Error(), proto.GQLConnectionError)
					continue
				}
			}

			conn.Events <- &EventConnectionInit{
				Parameters: value,
			}
		case proto.GQLStart:
			payload := &proto.PayloadOperation{}
			err := json.Unmarshal(payloadBytes.Bytes, payload)
			if err != nil {
				conn.Outgoing <- proto.NewMessage("", err.Error(), proto.GQLError)
				continue
			}
			conn.Events <- &EventOperationStart{
				ID:      msg.ID,
				Payload: payload,
			}
		case proto.GQLStop:
			conn.Events <- &EventOperationStop{
				ID: msg.ID,
			}
		case proto.GQLConnectionTerminate:
			conn.Events <- &EventConnectionTerminate{}
		}
	}
}

// WriteLoop writes messages to websocket
func (conn *Connection) WriteLoop(ws *websocket.Conn) {
	for {
		select {
		case o, ok := <-conn.Outgoing:
			if !ok {
				_ = ws.Close()
				conn.Events <- &EventConnectionWriteClosed{}
			}
			if o.Type == proto.GQLConnectionClose {
				if o.Payload != nil && o.Payload.Value != nil {
					_ = ws.WriteJSON(proto.NewMessage("", o.Payload, proto.GQLConnectionError))
				}
				_ = ws.Close()
				conn.Events <- &EventConnectionWriteClosed{}
				return
			}
			err := ws.WriteJSON(o)
			if err != nil {
				_ = ws.Close()
				conn.Events <- &EventConnectionWriteClosed{}
				return
			}
		case <-conn.Context.Done():
			_ = ws.Close()
			conn.Events <- &EventConnectionWriteClosed{}
			return
		}
	}
}

// MakeAST helper function to parse request into graphql AST
func MakeAST(query string, schema *graphql.Schema) (*ast.Document, *graphql.Result) {
	src := source.NewSource(&source.Source{
		Body: []byte(query),
		Name: "GraphQL request",
	})
	AST, err := parser.Parse(parser.ParseParams{Source: src})
	if err != nil {
		return AST, &graphql.Result{
			Errors: gqlerrors.FormatErrors(err),
		}
	}
	validationResult := graphql.ValidateDocument(schema, AST, nil)

	if !validationResult.IsValid {
		return AST, &graphql.Result{
			Errors: validationResult.Errors,
		}
	}
	return AST, nil
}

// EventLoop is an event reactor
func (conn *Connection) EventLoop() {
	for raw := range conn.Events {
		if !conn.processEvents(raw) {
			break
		}
	}
	close(conn.Events)
	close(conn.Outgoing)
	conn.Context.Complete()
	if conn.Server.OnDisconnect != nil {
		_ = conn.Server.OnDisconnect(conn.Context)
	}
}

// nolint:gocyclo // cyclomatic complexity 22 of func `(*Connection).processEvents` is high (> 15)
func (conn *Connection) processEvents(raw Event) bool {
	var err error
	switch evt := raw.(type) {
	case *EventConnectionInit:
		if conn.Server.OnConnect != nil {
			err = conn.Server.OnConnect(conn.Context, evt.Parameters)
			if err != nil {
				conn.Outgoing <- proto.NewMessage("", err.Error(), proto.GQLConnectionError)
				return true
			}
		}
		conn.Outgoing <- proto.NewMessage("", nil, proto.GQLConnectionAck)
		conn.Outgoing <- proto.NewMessage("", nil, proto.GQLConnectionKeepAlive)
	case *EventOperationStart:
		ctx := mutcontext.CreateNewCancel(context.WithCancel(conn.Context))
		if conn.Server.OnOperation != nil {
			err = conn.Server.OnOperation(conn.Context, ctx, evt.Payload)
			if err != nil {
				conn.Outgoing <- proto.NewMessage(evt.ID, err.Error(), proto.GQLError)
				return true
			}
		}
		conn.Subscriptions[evt.ID] = &Subscription{
			ID:      evt.ID,
			Payload: evt.Payload,
			Context: ctx,
		}
		atomic.AddInt32(&conn.StopCounter, -1)
		go conn.eventOperationStart(conn.Subscriptions[evt.ID], evt)
	case *EventOperationStop:
		if sub, ok := conn.Subscriptions[evt.ID]; ok {
			_ = sub.Context.Cancel()
		}
	case *EventOperationComplete:
		atomic.AddInt32(&conn.StopCounter, 1)
		if atomic.LoadInt32(&conn.StopCounter) >= 2 {
			return false
		}

		conn.Outgoing <- proto.NewMessage(evt.ID, nil, proto.GQLComplete)
		sub := conn.Subscriptions[evt.ID]
		if conn.Server.OnOperationDone != nil {
			err = conn.Server.OnOperationDone(conn.Context, sub.Context, sub.Payload)
			if err != nil {
				conn.Outgoing <- proto.NewMessage("", err.Error(), proto.GQLConnectionClose)
			}
		}
		delete(conn.Subscriptions, evt.ID)
	case *EventConnectionTerminate:
		conn.Outgoing <- proto.NewMessage("", nil, proto.GQLConnectionClose)
	case *EventConnectionWriteClosed:
		atomic.AddInt32(&conn.StopCounter, 1)
		if conn.TickCloser != nil {
			conn.TickCloser.Stop()
		}
		go func() {
			for range conn.Outgoing {
			}
		}()
		if atomic.LoadInt32(&conn.StopCounter) >= 2 {
			return false
		}
	case *EventConnectionReadClosed:
		_ = conn.Context.Cancel()
		atomic.AddInt32(&conn.StopCounter, 1)
		if atomic.LoadInt32(&conn.StopCounter) >= 2 {
			return false
		}
	case *EventConnectionTimerClosed:
		atomic.AddInt32(&conn.StopCounter, 1)
		if atomic.LoadInt32(&conn.StopCounter) >= 2 {
			return false
		}
	}
	return true
}

func (conn *Connection) eventOperationStart(sub *Subscription, evt *EventOperationStart) {
	AST, res := MakeAST(sub.Payload.Query, conn.Server.Schema)
	if res != nil {
		conn.Outgoing <- proto.NewMessage(sub.ID, res, proto.GQLData)
		conn.Events <- &EventOperationComplete{
			ID: evt.ID,
		}
		return
	}

	isSub := false
	for _, n := range AST.Definitions {
		if n.GetKind() == kinds.OperationDefinition {
			op, ok := n.(*ast.OperationDefinition)
			if !ok {
				continue
			}
			if op.Operation == ast.OperationTypeSubscription {
				isSub = true
				break
			}
		}
	}

	for {
		res := graphql.Execute(graphql.ExecuteParams{
			Schema:        *conn.Server.Schema,
			Root:          nil,
			AST:           AST,
			OperationName: sub.Payload.OperationName,
			Args:          sub.Payload.Variables,
			Context:       sub.Context,
		})

		conn.Outgoing <- proto.NewMessage(sub.ID, res, proto.GQLData)

		if len(res.Errors) > 0 || !isSub || sub.Context.Err() != nil || sub.Context.Completed() {
			break
		}
	}
	conn.Events <- &EventOperationComplete{
		ID: evt.ID,
	}
}

// ServePlainHTTP serves plain http request
// nolint:gocyclo // cyclomatic complexity 16 of func `(*Server).ServePlainHTTP` is high (> 15)
func (server *Server) ServePlainHTTP(ctx mutcontext.MutableContext, w http.ResponseWriter, r *http.Request) {
	if server.IgnorePlainHTTP {
		if server.OnPlainFail != nil {
			server.OnPlainFail(ctx, r, w, common.ErrPlainHTTPIgnored)
		}
		return
	}

	var result interface{}
	defer func() {
		if result != nil {
			err := json.NewEncoder(w).Encode(result)
			if err != nil && server.OnPlainFail != nil {
				server.OnPlainFail(ctx, r, w, err)
			}
		}
	}()

	var err error
	if server.OnConnect != nil {
		err = server.OnConnect(ctx, nil)
		if err != nil {
			result = err.Error()
			return
		}
	}

	body, errRead := ioutil.ReadAll(r.Body)
	if errRead != nil {
		if server.OnPlainFail != nil {
			server.OnPlainFail(ctx, r, w, errRead)
		}
		return
	}

	payload := &proto.PayloadOperation{}
	if err = json.Unmarshal(body, payload); err != nil {
		if server.OnPlainFail != nil {
			server.OnPlainFail(ctx, r, w, err)
		}
		return
	}

	opCtx := mutcontext.CreateNewCancel(context.WithCancel(ctx))
	if server.OnOperation != nil {
		err = server.OnOperation(ctx, opCtx, payload)
		if err != nil {
			result = err.Error()
			return
		}
	}

	params := graphql.Params{
		Schema:         *server.Schema,
		RequestString:  payload.Query,
		VariableValues: payload.Variables,
		OperationName:  payload.OperationName,
		Context:        ctx,
	}

	result = graphql.Do(params)

	if server.OnOperationDone != nil {
		_ = server.OnOperationDone(ctx, opCtx, payload)
	}
	if server.OnDisconnect != nil {
		_ = server.OnDisconnect(ctx)
	}
}

// ServeWebsocketHTTP serves websocket http request
func (server *Server) ServeWebsocketHTTP(ctx mutcontext.MutableContext, w http.ResponseWriter, r *http.Request) {
	ws, err := server.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		if server.OnPlainFail != nil {
			server.OnPlainFail(ctx, r, w, err)
		}
		return
	}
	if ws.Subprotocol() != "graphql-ws" {
		_ = ws.Close()
		return
	}

	var ticker *TickCloser
	if server.KeepAlive > 0 {
		ticker = &TickCloser{
			Ticker:  time.NewTicker(server.KeepAlive),
			Stopped: make(chan bool),
		}
	}

	conn := &Connection{
		Server:        server,
		Subscriptions: make(map[interface{}]*Subscription),
		Context:       ctx,
		Outgoing:      make(chan *proto.Message),
		Events:        make(chan Event),
		TickCloser:    ticker,
	}

	go conn.ReadLoop(ws)
	go conn.WriteLoop(ws)
	go conn.EventLoop()

	if conn.TickCloser != nil {
		atomic.AddInt32(&conn.StopCounter, -1)
		go func() {
			for {
				select {
				case <-conn.TickCloser.Stopped:
					conn.Events <- &EventConnectionTimerClosed{}
					return
				case <-conn.TickCloser.Ticker.C:
					conn.Outgoing <- proto.NewMessage("", nil, proto.GQLConnectionKeepAlive)
				}
			}
		}()
	}
}

// ServeHTTP is an http.Handler entrypoint
func (server *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := mutcontext.CreateNewCancel(context.WithCancel(context.Background()))
	ctx.Set(common.KeyHTTPRequest, r)

	if server.OnPlainInit != nil {
		server.OnPlainInit(ctx, r, w)
	}
	h := r.Header

	if h.Get("Connection") == "" || h.Get("Upgrade") == "" || server.Upgrader == nil {
		server.ServePlainHTTP(ctx, w, r)
		return
	}

	server.ServeWebsocketHTTP(ctx, w, r)
}
