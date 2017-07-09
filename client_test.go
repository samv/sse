package sse_test

// test scenarios:
import (
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/samv/sse"
)

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
			log.Printf("Close client connections")
			testServer.Server.CloseClientConnections()
			log.Printf("Close server")
			testServer.Server.Close()
			log.Printf("done")
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

func TestClientConnect(t *testing.T) {
	testServer := newTestSSEServer()
	testServer.Start()

	client := sse.NewSSEClient()
	err := client.GetStream(testServer.Server.URL)
	if err != nil {
		t.Fatalf("Failed to connect to SSE server: %v", err)
	}
	testServer.objectFeed <- map[string]interface{}{"hello": "realtime"}

	// client.Close()
	// hangs
	//testServer.Stop()
	//log.Printf("stopped")
}
