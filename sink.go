package sse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// EventFeed is a type for something that can return events in a form
// this API can write them to the write
type EventFeed interface {
	GetEventChan(clientCloseChan <-chan struct{}) <-chan SinkEvent
}

// SinkEvent is a generic type for things which can be marshalled to
// bytes.  They might also implement any of the below interfaces to
// control behavior.
type SinkEvent interface {
	GetData() ([]byte, error)
}

// EventSink is a structure used by the event sink writer
type EventSink struct {
	w           http.ResponseWriter
	flusher     http.Flusher
	feed        <-chan SinkEvent
	closeNotify <-chan bool
	closedChan  chan struct{}
}

// SinkEvents is an more-or-less drop-in replacement for a responder
// in a net/http response handler.  It handles all the SSE protocol
// for you - just feed it events.
func SinkEvents(w http.ResponseWriter, code int, feed EventFeed) error {
	sink, err := NewEventSink(w, feed)
	if err != nil {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return err
	}
	sink.Respond(code)
	return sink.Sink()
}

// NewEventSink returns an Event
func NewEventSink(w http.ResponseWriter, feed EventFeed) (*EventSink, error) {
	sink := &EventSink{
		w: w,
	}
	var ok bool
	sink.flusher, ok = sink.w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("ResponseWriter %v does not implement http.Flusher", w)
	}

	// Listen to the closing of the http connection via the CloseNotifier
	closeNotifier, ok := sink.w.(http.CloseNotifier)
	if !ok {
		return nil, fmt.Errorf("ResponseWriter %v does not implement http.CloseNotifier", w)
	}
	sink.closeNotify = closeNotifier.CloseNotify()

	// pass the message via channel-close semantics
	sink.closedChan = make(chan struct{})
	sink.feed = feed.GetEventChan(sink.closedChan)

	return sink, nil
}

// Respond sets up the event channel - sends the HTTP headers, and
// starts writing a response.  The keepalive timer is started.
func (sink *EventSink) Respond(code int) {
	// Set essential headers related to event streaming.  Set your own headers prior to
	// calling NewEventSink if you need more.
	sink.w.Header().Set("Content-Type", "text/event-stream")
	sink.w.Header().Set("Cache-Control", "no-cache")
	sink.w.Header().Set("Connection", "keep-alive")

	sink.w.WriteHeader(code)
	sink.flusher.Flush()
}

func (sink *EventSink) closeFeed() {
	if sink.closedChan != nil {
		close(sink.closedChan)
		sink.closedChan = nil
	}
}

// Sink is the main event sink loop for the EventSink.  Caller to
// provide the goroutine if required.
func (sink *EventSink) Sink() error {
	var sinkErr error
sinkLoop:
	for {
		select {
		case <-sink.closeNotify:
			sink.closeFeed()
			// wait for eventFeed to empty/close instead of breaking out here

		case event, ok := <-sink.feed:
			if !ok {
				sink.feed = nil
				break sinkLoop
			}
			if sinkErr = sink.sinkEvent(event); sinkErr != nil {
				sink.closeFeed()
			}
		}
	}
	sink.safeClose()
	return sinkErr
}

// prevent deadlocks by emptying channels instead of letting them block on write
func (sink *EventSink) safeClose() {
	sink.closeFeed()
	if sink.feed != nil {
		go func() {
			for {
				_, ok := <-sink.feed
				if !ok {
					break
				}
			}
		}()
	}
}

func (sink *EventSink) sinkEvent(event SinkEvent) error {
	var writeErr error
	eventBody, dataErr := event.GetData()
	if dataErr != nil {
		Logger.Printf("Error marshaling a %T (%v) via GetData; %v", event, event, dataErr)
	}

	// returning an empty interface value permits options like keepalive and retry
	// to be specified without generating an actual event
	if len(eventBody) != 0 {
		writeErr = writeDataLines(sink.w, eventBody)
	}

	// a newline delimits events, but is also safe to send if no event was sent.
	if writeErr == nil {
		_, writeErr = sink.w.Write(endOfLine)
		sink.flusher.Flush()
	}

	return writeErr
}

// writeDataLines writes the data as an SSE event, making sure not to
// violate the protocol by inadvertantly emitting either of the two
// reserved characters: \n and \r
func writeDataLines(w io.Writer, data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var err error
	for (err == nil) && scanner.Scan() {
		line := scanner.Bytes()
		// I care more about SSE protocol conformance than junk input;
		// throw away everything from the first \r on the line
		if pos := bytes.IndexRune(line, '\r'); pos > -1 {
			line = line[:pos]
		}
		_, err = w.Write(DataHeader)
		if err == nil {
			_, err = w.Write(fieldDelim)
		}
		if err == nil {
			_, err = w.Write(line)
		}
		if err == nil {
			_, err = w.Write(endOfLine)
		}
	}
	return err
}
