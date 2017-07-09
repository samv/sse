package sse

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/samv/sse/mock"
)

type mockEventFeed struct {
	evChan     chan SinkEvent
	closedChan <-chan struct{}
	closed     bool
}

func newMockEventFeed(size int) *mockEventFeed {
	return &mockEventFeed{
		evChan: make(chan SinkEvent, size),
	}
}

// smallest sleep possible for tests
const JIFFY = time.Nanosecond

// GetEventChan satisfies the EventFeed interface
func (evf *mockEventFeed) GetEventChan(closedChan <-chan struct{}) <-chan SinkEvent {
	Logger.Printf("got event chan.")
	evf.closedChan = closedChan
	return evf.evChan
}

func (evf *mockEventFeed) isClosed() bool {
	select {
	case _, ok := <-evf.closedChan:
		evf.closed = !ok
	default:
	}
	return evf.closed
}

func (evf *mockEventFeed) feedEvent(ev SinkEvent) {
	if !evf.isClosed() {
		evf.evChan <- ev
	}
}

func (evf *mockEventFeed) wait() {
	for {
		_, ok := <-evf.closedChan
		if !ok {
			close(evf.evChan)
			evf.evChan = nil
			return
		}
	}
}

func TestSink(t *testing.T) {
	Logger.Println("a")
	rw := mock.NewResponseWriter()

	evFeed := newMockEventFeed(1)

	Logger.Println("b")
	sink, err := NewEventSink(rw, evFeed)
	if !assert.NoError(t, err) {
		return
	}

	sink.Respond(200)

	Logger.Println("c")
	var sinkDone sync.WaitGroup
	go func() {
		sinkDone.Add(1)
		sink.Sink()
		sinkDone.Done()
	}()

	evFeed.feedEvent(&Event{Data: []byte("hello, world\n")})
	evFeed.feedEvent(&Event{Data: []byte("howdy,\nfolks\n")})
	evFeed.feedEvent(&Event{Data: []byte("T WORLD\r\nOHAI\r\n")})

	expected := []*Event{
		{Type: "message", Data: []byte("hello, world")},
		{Type: "message", Data: []byte("howdy,\nfolks")},
		{Type: "message", Data: []byte("T WORLD\nOHAI")},
	}

	assert.False(t, evFeed.isClosed())

	// response writer closes (it still receives data though, convenient for this test)
	rw.Close()

	// this step is only necessary because this mock feed is not its own goroutine.
	evFeed.wait()
	assert.True(t, evFeed.isClosed())

	// test that the sink exits
	sinkDone.Wait()

	// now check the response is valid SSE protocol
	responseBytes := rw.ResponseBytes()
	assertEmitsEvents(t, expected, responseBytes, "TestSink basic")
}

// TestSinkDrains checks that a Sink will provide channel backpressure when closed
func TestSinkDrains(t *testing.T) {
	evFeed := newMockEventFeed(0)
	rw := mock.NewResponseWriter()
	sink, err := NewEventSink(rw, evFeed)
	if !assert.NoError(t, err) {
		return
	}
	sink.Respond(200)

	// start a sink and feed it some stuff
	var sinkDone sync.WaitGroup
	go func() {
		sinkDone.Add(1)
		sink.Sink()
		sinkDone.Done()
	}()

	// a misbehaved feed will not check the closed channel promptly
	for i := 0; i < 100; i++ {
		evFeed.feedEvent(&Event{Data: []byte(fmt.Sprintf("event #%d", i+1))})
		if i == 50 {
			rw.Close()
			evFeed.wait()
		} else {
			time.Sleep(JIFFY)
		}
	}
	assert.True(t, evFeed.isClosed())

	sinkDone.Wait()
	reader := bytes.NewReader(rw.ResponseBytes())
	dummyOrigin := "http://test.example.com/"
	protocol := newEventStreamReader(reader, dummyOrigin)
	var eventCount int
	for _ = range protocol.decodeChan() {
		eventCount++
	}
	Logger.Printf("Read %d event(s)", eventCount)

	if eventCount < 50 {
		t.Errorf("Not enough events written; expected 50, saw %d", eventCount)
	} else if eventCount == 100 {
		t.Error("All events written, bad test")
	}
}
