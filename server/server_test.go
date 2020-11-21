package server

import (
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/graphql-go/graphql"

	"github.com/lexycore/wsgraphql/mutcontext"
	"github.com/lexycore/wsgraphql/proto"
)

const (
	jsonDataFoo34 = `{"data":{"foo":34}}`
)

func makeRequest(t *testing.T, listener net.Listener, query string) string {
	url := "http://127.0.0.1:" + strconv.FormatUint(uint64(listener.Addr().(*net.TCPAddr).Port), 10)

	reader := strings.NewReader(query)
	resp, err := http.Post(url, "application/json", reader)
	if err != nil {
		t.Error("error during request", err)
		return ""
	}
	bs, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Error("error during reading", err)
		return ""
	}
	_ = resp.Body.Close()
	return strings.TrimSpace(string(bs))
}

func makeConn(t *testing.T, listener net.Listener) *websocket.Conn {
	url := "ws://127.0.0.1:" + strconv.FormatUint(uint64(listener.Addr().(*net.TCPAddr).Port), 10)

	c, _, err := websocket.DefaultDialer.Dial(url, http.Header{
		"Sec-WebSocket-Protocol": []string{"graphql-ws"},
	})
	if err != nil {
		t.Error("error opening websocket", err)
	}

	return c
}

func makeServer(schema *graphql.Schema) net.Listener {
	srv := &Server{
		Upgrader: &websocket.Upgrader{Subprotocols: []string{"graphql-ws"}},
		OnPlainFail: func(globalctx mutcontext.MutableContext, r *http.Request, w http.ResponseWriter, err error) {
			_, _ = w.Write([]byte(err.Error()))
		},
		Schema:    schema,
		KeepAlive: time.Second * 20,
	}

	listener, _ := net.Listen("tcp", ":0")

	go func() {
		_ = http.Serve(listener, srv)
	}()
	return listener
}

func TestServer_ServeHTTP_singular(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"foo": &graphql.Field{
					Type: graphql.Int,
					Resolve: func(p graphql.ResolveParams) (i interface{}, e error) {
						return 34, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Error("unexpected schema error", err)
	}
	listener := makeServer(&schema)
	resp := makeRequest(t, listener, `{"query": "query { foo }"}`)
	if resp != jsonDataFoo34 {
		t.Error("invalid response", resp)
	}

	_ = listener.Close()
}

func TestServer_ServeHTTP_websocket_query(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"foo": &graphql.Field{
					Type: graphql.Int,
					Resolve: func(p graphql.ResolveParams) (i interface{}, e error) {
						return 34, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Error("unexpected schema error", err)
	}
	listener := makeServer(&schema)
	conn := makeConn(t, listener)

	wait := make(chan bool)
	go func() {
		ack := &proto.Message{}
		err2 := conn.ReadJSON(ack)
		if err2 != nil {
			t.Error("error", err2)
		}
		if ack.Type != proto.GQLConnectionAck {
			t.Error("Invalid ack")
		}

		ka := &proto.Message{}
		err2 = conn.ReadJSON(ka)
		if err2 != nil {
			t.Error("error", err2)
		}
		if ka.Type != proto.GQLConnectionKeepAlive {
			t.Error("Invalid keepalive")
		}

		data := &proto.Message{}
		err2 = conn.ReadJSON(data)
		if err2 != nil {
			t.Error("error", err2)
		}
		if data.Type != proto.GQLData {
			t.Error("Invalid data type")
		}
		if string(data.Payload.Bytes) != jsonDataFoo34 {
			t.Error("invalid response", string(data.Payload.Bytes))
		}

		compl := &proto.Message{}
		err2 = conn.ReadJSON(compl)
		if err2 != nil {
			t.Error("error", err2)
		}
		if compl.Type != proto.GQLComplete {
			t.Error("Invalid complete")
		}
		close(wait)
	}()

	_ = conn.WriteJSON(map[string]interface{}{
		"type":    proto.GQLConnectionInit,
		"payload": 123,
	})

	_ = conn.WriteJSON(map[string]interface{}{
		"id":   "1",
		"type": proto.GQLStart,
		"payload": map[string]interface{}{
			"query": `query { foo }`,
		},
	})

	<-wait
	_ = conn.WriteJSON(map[string]interface{}{
		"type": proto.GQLConnectionTerminate,
	})
	_ = listener.Close()
}

func TestServer_ServeHTTP_websocket_subscription(t *testing.T) {
	cleanupCalled := false

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"dummy": &graphql.Field{
					Type: graphql.Int,
				},
			},
		}),
		Subscription: graphql.NewObject(graphql.ObjectConfig{
			Name: "RootSubscription",
			Fields: graphql.Fields{
				"foo": &graphql.Field{
					Type: graphql.Int,
					Resolve: func(p graphql.ResolveParams) (i interface{}, e error) {
						ctx := p.Context.(mutcontext.MutableContext)
						v := ctx.Value("idx")
						if v == nil {
							ctx.Set("idx", 6)
							ctx.SetCleanup(func() {
								cleanupCalled = true
							})
							v = 6
						}
						idx := v.(int)
						idx--
						ctx.Set("idx", idx)
						if idx <= 0 {
							ctx.Complete()
						}
						return 34, nil
					},
				},
			},
		}),
	})
	if err != nil {
		t.Fatal("unexpected schema error", err)
	}
	listener := makeServer(&schema)
	conn := makeConn(t, listener)

	wait := make(chan bool)
	go func() {
		ack := &proto.Message{}
		err2 := conn.ReadJSON(ack)
		if err2 != nil {
			t.Error("error", err2)
		}
		if ack.Type != proto.GQLConnectionAck {
			t.Error("Invalid ack")
		}

		ka := &proto.Message{}
		err2 = conn.ReadJSON(ka)
		if err2 != nil {
			t.Error("error", err2)
		}
		if ka.Type != proto.GQLConnectionKeepAlive {
			t.Error("Invalid keepalive")
		}

		for i := 0; i < 6; i++ {
			data := &proto.Message{}
			err2 = conn.ReadJSON(data)
			if err2 != nil {
				t.Error("error", err2)
			}
			if data.Type != proto.GQLData {
				t.Error("Invalid data type")
			}
			if string(data.Payload.Bytes) != jsonDataFoo34 {
				t.Error("invalid response", string(data.Payload.Bytes))
			}
		}

		compl := &proto.Message{}
		err2 = conn.ReadJSON(compl)
		if err2 != nil {
			t.Error("error", err2)
		}
		if compl.Type != proto.GQLComplete {
			t.Error("Invalid complete")
		}
		close(wait)
	}()

	_ = conn.WriteJSON(map[string]interface{}{
		"type":    proto.GQLConnectionInit,
		"payload": 123,
	})

	_ = conn.WriteJSON(map[string]interface{}{
		"id":   "1",
		"type": proto.GQLStart,
		"payload": map[string]interface{}{
			"query": `subscription { foo }`,
		},
	})

	<-wait
	_ = conn.WriteJSON(map[string]interface{}{
		"type": proto.GQLConnectionTerminate,
	})
	_ = listener.Close()
	if !cleanupCalled {
		t.Error("Cleanup function was not called")
	}
}
