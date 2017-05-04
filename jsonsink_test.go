package sse

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/samv/sse/mock"
)

type mockJSONEventFeed struct {
	mockEventFeed
	evChan chan interface{}
}

func newMockJSONEventFeed(size int) *mockJSONEventFeed {
	return &mockJSONEventFeed{
		evChan: make(chan interface{}, size),
	}
}

// GetEventChan satisfies the EventFeed interface
func (evf *mockJSONEventFeed) GetEventChan(closedChan <-chan struct{}) <-chan interface{} {
	evf.closedChan = closedChan
	return evf.evChan
}

func (evf *mockJSONEventFeed) feedEvent(ev interface{}) {
	if !evf.isClosed() {
		evf.evChan <- ev
	}
}

func (evf *mockJSONEventFeed) wait() {
	for {
		_, ok := <-evf.closedChan
		if !ok {
			close(evf.evChan)
			evf.evChan = nil
			return
		}
	}
}

func TestJSONSink(t *testing.T) {
	rw := mock.NewResponseWriter()

	evFeed := newMockJSONEventFeed(1)

	sink, err := NewEventSink(rw, NewJSONEncoderFeed(evFeed))
	if !assert.NoError(t, err) {
		return
	}

	sink.Respond(200)

	var sinkDone sync.WaitGroup
	go func() {
		sinkDone.Add(1)
		sink.Sink()
		sinkDone.Done()
	}()

	evFeed.feedEvent(map[string]interface{}{"hello": "world"})
	evFeed.feedEvent([]string{"one", "two", "three"})
	evFeed.feedEvent(struct {
		Key int `json:"key"`
	}{Key: 123})

	expected := []*Event{
		{Type: "message", Data: []byte(`{"hello":"world"}`)},
		{Type: "message", Data: []byte(`["one","two","three"]`)},
		{Type: "message", Data: []byte(`{"key":123}`)},
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
	assertEmitsEvents(t, expected, responseBytes, "TestJSONSink basic")
}
