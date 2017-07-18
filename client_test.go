package sse_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	poisoned        bool
}

func (testServer *testSSEServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Printf("hit from %s; url=%s header=%v", r.RemoteAddr, r.URL, r.Header)
	defer log.Printf("testServer: hit from %s done", r.RemoteAddr)
	if testServer.poisoned {
		http.Error(w, "Who are you? Crank caller!", http.StatusForbidden)
	} else if err := sse.SinkJSONEvents(w, 200, testServer); err != nil {
		// I hereby declare the various Sink* functions will only return an error if no response
		// has yet been written.
		http.Error(w, "failed to sink JSON events", http.StatusInternalServerError)
	}
}

func (testServer *testSSEServer) Poison() {
	testServer.poisoned = true
}

func (testServer *testSSEServer) Start() string {
	testServer.Server = httptest.NewServer(testServer)
	testServer.wg.Add(1)
	testServer.closedChan = make(chan struct{})
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
	log.Printf("returning eventChan: %v (close=%v)", testServer.objectFeed, clientCloseChan)
	testServer.clientCloseChan = clientCloseChan
	return testServer.objectFeed
}

func newTestSSEServer() *testSSEServer {
	events := make(chan interface{})
	testServer := &testSSEServer{
		objectFeed: events,
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

// TestClientOpenClose tests that clients can notice connections
// opening and closing
func TestClientOpenClose(t *testing.T) {
	testServer := newTestSSEServer()
	testServer.Start()

	client := sse.NewSSEClient(sse.ReconnectTime(10 * time.Millisecond))
	err := client.GetStream(testServer.Server.URL)
	if err != nil {
		t.Fatalf("Failed to connect to SSE server: %v", err)
	}
	go func() {
		log.Printf("clientTest: sinking message")
		testServer.objectFeed <- map[string]interface{}{"one": "message"}
		time.Sleep(50 * time.Millisecond)
		testServer.objectFeed = make(chan interface{})
		testServer.Server.CloseClientConnections()
		log.Printf("clientTest: connections closed")
		time.Sleep(100 * time.Millisecond)
		log.Printf("clientTest: sinking second message")
		testServer.objectFeed <- map[string]interface{}{"two": "messages"}
		log.Printf("clientTest: sunk")
	}()

	var messages []interface{}
	var opens, closes int

	timer := time.NewTimer(2 * time.Second)
testLoop:
	for {
		select {
		case err, ok := <-client.Errors():
			if ok {
				t.Fatalf("Read an error: %v", err)
			}
		case open, ok := <-client.Opens():
			if ok {
				if open {
					log.Printf("clientTest: channel open")
					opens++
				} else {
					log.Printf("clientTest: channel closed")
					closes++
				}
			}
		case msg, ok := <-client.Messages():
			if ok {
				log.Printf("clientTest: read a message: %v", msg)
				messages = append(messages, msg)
				if len(messages) > 1 {
					timer.Reset(time.Millisecond)
				}
			} else {
				log.Printf("clientTest: messages channel closed")
				break testLoop
			}
		case <-timer.C:
			break testLoop
		}
	}

	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, saw %d", len(messages))
	}
	if opens != 2 {
		t.Errorf("Expected 2 opens, saw %d", opens)
	}
	if closes != 1 {
		t.Errorf("Expected 1 close, saw %d", closes)
	}

	client.Close()
	log.Printf("clientTest: closed client again")

	testServer.Stop()
	log.Printf("stopped")
}

// TestClientError tests that clients can read connection errors
// This test may be timing dependent.
func TestClientError(t *testing.T) {
	testServer := newTestSSEServer()
	testServer.Start()
	testServer.Poison()

	client := sse.NewSSEClient()
	err := client.GetStream(testServer.Server.URL)
	if err != nil {
		t.Fatalf("Failed to connect to SSE server: %v", err)
	}
	log.Printf("clientTest: sinking message")
	go func() {
		testServer.objectFeed <- map[string]interface{}{"hello": "realtime"}
	}()

	var readError error

	timer := time.NewTimer(100 * time.Millisecond)
	select {
	case err, ok := <-client.Errors():
		log.Printf("Read an error? err=%v (%T) ok=%v", err, err, ok)
		if ok {
			readError = err
		} else {
			t.Error("Read Errors close unexpectedly")
		}
	case _, ok := <-client.Messages():
		if ok {
			t.Error("Read Message unexpectedly")
		}
	case <-timer.C:
		t.Error("test timed out")
	}

	if readError == nil {
		t.Fatal("Failed to read error via client")
	}
	if strings.Index(readError.Error(), "403") < 0 {
		t.Fatalf("Expected '403' in %s", readError.Error())
	}

	log.Printf("clientTest: closing client")
	client.Close()

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

// test scenarios:
//  "happy" case - connect to a server
//    * client reading just messages
//    * client watching errors
//    * client watching for "open"

//  "reconnect" cases -
//    * reconnects by default
//    * with a negotiated retry
//    * client watching for "closed"
