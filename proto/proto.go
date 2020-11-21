// Package proto is an implementation of GraphQL over WebSocket Protocol by apollographql
// https://github.com/apollographql/subscriptions-transport-ws/blob/master/PROTOCOL.md
package proto

import "encoding/json"

// OperationType is OperationMessage type
type OperationType string

// Pseudo types, used in events to signal state changes
const (
	GQLUnknown OperationType = ""

	GQLConnectionClose = "connection_close"

	// Client to Server types

	GQLConnectionInit      = "connection_init"
	GQLStart               = "start"
	GQLStop                = "stop"
	GQLConnectionTerminate = "connection_terminate"

	// Server to Client

	GQLConnectionError     = "connection_error"
	GQLConnectionAck       = "connection_ack"
	GQLData                = "data"
	GQLError               = "error"
	GQLComplete            = "complete"
	GQLConnectionKeepAlive = "ka"
)

// NewMessage creates new operation message
func NewMessage(id interface{}, payload interface{}, t OperationType) *Message {
	return &Message{
		ID:      id,
		Payload: &PayloadBytes{Value: payload},
		Type:    t,
	}
}

// Message generic type
type Message struct {
	ID      interface{}   `json:"id"`
	Payload *PayloadBytes `json:"payload"`
	Type    OperationType `json:"type"`
}

// PayloadOperation for operation parametrization
type PayloadOperation struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
	OperationName string                 `json:"operationName"`
}

// PayloadData for result of operation execution
type PayloadData struct {
	Data   interface{} `json:"data"`
	Errors []error     `json:"errors"`
}

// PayloadBytes combines functionality of interface{} and json.RawMessage
type PayloadBytes struct {
	Bytes []byte
	Value interface{}
}

// UnmarshalJSON saves bytes for later deserialization, just as json.RawMessage
func (payload *PayloadBytes) UnmarshalJSON(b []byte) error {
	payload.Bytes = b
	return nil
}

// MarshalJSON serializes interface value
func (payload *PayloadBytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(payload.Value)
}
