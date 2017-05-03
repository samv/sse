package sse

// EventFeed is a type for something that can return events in a form
// this API can write them to the write
type EventFeed interface {
	GetEventChan(clientCloseChan chan<- struct{}) <-chan SinkEvent
}

// SinkEvent is a generic type for things which can be marshalled to
// bytes.  They might also implement any of the below interfaces to
// control behavior.
type SinkEvent interface {
	GetData() ([]byte, error)
}

// NamedEvent is for responses which are not standard 'message' events
type NamedEvent interface {
	EventName() string
}

// EventSink is a structure used by the event sink writer
type EventSink struct {
	w           http.ResponseWriter
	flush       http.Flusher
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
	err = sink.Respond(code)
	if err != nil {
		return err
	}
	return sink.Sink()
}

// NewEventSink returns an Event
func NewEventSink(w http.ResponseWriter, feed EventFeed) (*EventSink, error) {
	sink := &EventSink{
		w: w,
	}
	var ok bool
	sink.flush, ok = sink.w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("ResponseWriter %v does not implement http.Flusher", w)
	}

	// Listen to the closing of the http connection via the CloseNotifier
	sink.connReset, ok = sink.w.(http.CloseNotifier).CloseNotify()
	if !ok {
		return nil, fmt.Errorf("ResponseWriter %v does not implement http.CloseNotifier", w)
	}

	// pass the message via channel-close semantics
	sink.closedChan = make(chan struct{})
	sink.feed = feed.GetEventChan(sink.closedChan)

	return sink
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
	sink.flush.Flush()
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
	for {
		select {
		case <-sink.closeNotify:
			sink.closeFeed()
			// wait for eventFeed to empty/close instead of breaking out here

		case event, ok := <-sink.feed:
			if !ok {
				sink.feed = nil
				break
			}
			if sinkErr = sink.sinkEvent(event); sinkErr != nil {
				sink.closeFeed()
			}
		}
	}
	sink.safeClose()
	return sinkErr
}

func (sink *EventSink) sinkEvent(event SinkEvent) error {

	var eventName = "message"
	if namedEvent, ok := event.(NamedEvent); ok {
		eventName = namedEvent.EventName()
		_, writeErr = sink.w.Write(EventHeader)
		if writeErr == nil {
			millis := []byte(strconv.Itoa(int(retry / time.Millisecond)))
			_, writeErr = sink.w.Write(append(millis, newLine...))
		}
	}

	if writeErr == nil {
		eventBody, dataErr = event.GetData()
		// returning an empty interface value permits options like keepalive and retry
		// to be specified without generating an actual event
		if len(eventBody) != 0 {
			writeErr = writeDataLines(w, eventBody)
		}
	}

	// a newline delimits events, but is also safe to send if no event was sent.
	if writeErr == nil {
		_, writeErr = sink.w.Write(newLine)
		f.Flush()
	}

	return writeErr
}

// writeLines writes the data as an SSE event, making sure not to
// violate the protocol by inadvertantly emitting either of the two
// reserved characters: \n and \r
func writeLines(w io.Writer, data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var err error
	for (err == nil) && scanner.Scan() {
		line := scanner.Bytes()
		// I care more about SSE protocol conformance than junk input;
		// throw away everything from the first \r on the line
		if pos := bytes.Index(line, '\r'); pos > -1 {
			line = line[:pos]
		}
		_, err = sink.w.Write(dataHeader)
		if err == nil {
			_, err = sink.w.Write(line)
		}
		if err == nil {
			_, err = sink.w.Write(newLine)
		}
	}
	return err
}
