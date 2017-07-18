package sse_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/samv/sse"
)

func init() {
	if os.Getenv("debug") != "" {
		sse.Logger.SetOutput(os.Stderr)
	}
}

// testSSEServer is a simple server that streams events and cannot
// (sensibly) handle more than one connection at a time
type testSSEServer struct {
	objectFeed      chan interface{}
	closedChan      chan struct{}
	clientCloseChan <-chan struct{}
	wg              sync.WaitGroup
	Server          *httptest.Server
}

func (testServer *testSSEServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("hit from %s; url=%s header=%v", r.RemoteAddr, r.URL, r.Header)
	defer log.Printf("testServer: hit from %s done", r.RemoteAddr)
	if err := sse.SinkJSONEvents(w, 200, testServer); err != nil {
		// I hereby declare the various Sink* functions will only return an error if no response
		// has yet been written.
		http.Error(w, "failed to sink JSON events", http.StatusInternalServerError)
	}
}

func (testServer *testSSEServer) Start() string {
	testServer.Server = httptest.NewServer(testServer)
	testServer.wg.Add(1)
	go testServer.watchClose()
	return testServer.Server.URL
}

func (testServer *testSSEServer) watchClose() {
	for {
		_, ok := <-testServer.closedChan
		if !ok {
			log.Printf("testServer: closing client connections")
			testServer.Server.CloseClientConnections()
			log.Printf("testServer: closing server")
			testServer.Server.Close()
			log.Printf("testServer: done")
			testServer.wg.Done()
			return
		}
	}
}

func (testServer *testSSEServer) Stop() {
	close(testServer.closedChan)
	testServer.wg.Wait()
}

func (testServer *testSSEServer) GetEventChan(clientCloseChan <-chan struct{}) <-chan interface{} {
	testServer.clientCloseChan = clientCloseChan
	return testServer.objectFeed
}

func newTestSSEServer() *testSSEServer {
	closedChan := make(chan struct{})
	events := make(chan interface{})
	testServer := &testSSEServer{
		objectFeed: events,
		closedChan: closedChan,
	}
	return testServer
}

// TestClientConnect tests that a client can connect to the server, and close again.
func TestClientConnect(t *testing.T) {
	testServer := newTestSSEServer()
	testServer.Start()

	client := sse.NewSSEClient()
	err := client.GetStream(testServer.Server.URL)
	if err != nil {
		t.Fatalf("Failed to connect to SSE server: %v", err)
	}
	log.Printf("clientTest: sinking message")
	testServer.objectFeed <- map[string]interface{}{"hello": "realtime"}

	var readEvent *sse.Event

	timer := time.NewTimer(100 * time.Millisecond)
	select {
	case ev, ok := <-client.Messages():
		if ok {
			readEvent = ev
		} else {
			t.Error("Client read early EOF")
		}
	case <-timer.C:
	}

	if readEvent == nil {
		t.Fatal("Failed to read event via client")
	}
	if bytes.Index(readEvent.Data, []byte("realtime")) < 0 {
		t.Errorf("Event wasn't what was expected, saw: %v", string(readEvent.Data))
	}

	client.Close()
	log.Printf("clientTest: closed client")

	//hangs
	testServer.Stop()
	log.Printf("stopped")
}

// test scenarios:
//  "happy" case - connect to a server
//    * client reading just messages
//    * client watching errors
//    * client watching for "open"

//  "reconnect" cases -
//    * reconnects by default
//    * with a negotiated retry
//    * client watching for "closed"
