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

// KeepAliver is for event sources (or responses) to specify the keep
// alive TTL which applies until the next event is read.
type KeepAliver interface {
	KeepAlive() time.Duration
}

// NamedEvent is for responses which are not standard 'message' events
type NamedEvent interface {
	EventName() string
}

// EventIDer is for responses which set the last event ID so that
// clients can specify it when they reconnect (this is automatic by
// the browser implementation of EventSource)
type EventIDer interface {
	EventID() string
}

// Resumer is for events which specify that they have a resume TTL
type GetRetrier interface {
	GetRetry() time.Duration
}

// default is to send a keepalive every minute.  Override by calling
// SetKeepAlive(), ideally before Sink()
const defaultKeepAlive = 60 * time.Second
const defaultKeepAlivePrefix = "sse.EventSink: "

// EventSink is a structure used by the event sink writer
type EventSink struct {
	sync.Mutex
	w           http.ResponseWriter
	flush       http.Flusher
	feed        <-chan SinkEvent
	closeNotify <-chan bool
	closedChan  chan struct{}

	// keepAlive-related
	keepAliveTime     time.Duration
	keepAliveTimer    *time.Timer
	KeepAlivePrefix   string
	KeepAliveShowInfo bool
	onlineSince       time.Time

	// records the last retry time set.
	retryTime time.Duration
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
	sink.SetKeepAlive(defaultKeepAlive)
	sink.KeepAlivePrefix = defaultKeepAlivePrefix
	sink.KeepAliveShowInfo = true

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

	sink.onlineSince = time.Now()
	sink.resetKeepAlive()
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
			} else {
				sink.resetKeepAlive()
			}

		case <-sink.keepAliveTimer.C:
			if sinkErr = sink.writeKeepAlive(); sinkErr != nil {
				sink.closeFeed()
			} else {
				sink.resetKeepAlive()
			}
		}
	}
	sink.safeClose()
	return sinkErr
}

func (sink *EventSink) resetKeepAlive() {
	sink.Lock()
	keepAlive := sink.keepAliveTime
	sink.Unlock()
	if keepAliveTime > time.Millisecond {
		sink.keepAliveTimer.Reset(keepAliveTime)
	}
}

func (sink *EventSink) writeKeepAlive() error {
	var keepAliveMsg bytes.Buffer
	keepAliveMsg.WriteRune(':')
	if len(sink.KeepAlivePrefix) > 0 {
		keepAliveMsg.WriteString(sink.KeepAlivePrefix)
	}
	if sink.KeepAliveShowInfo {
		now := time.Now()
		keepAliveMsg.WriteString(fmt.Sprintf(
			"server time is %v, online for %v", now, now.Sub(sink.onlineSince),
		))
	}
	keepAliveMsg.WriteRune('\n')
	_, err := sink.w.Write(keepAliveMsg.Bytes())
	return err
}

// SetKeepAlive sets the current keepalive time for an event sink.
func (sink *EventSink) GetKeepAlive() time.Duration {
	sink.Lock()
	keepAlive := sink.keepAliveTime
	sink.Unlock()
	return keepAlive
}

// SetKeepAlive sets the current keepalive time for an event sink.  It does not reset the timer;
// if you want to do that, call this
func (sink *EventSink) SetKeepAlive(keepAlive time.Duration) {
	sink.Lock()
	defer sink.Unlock()
	keepAlive := keepAliver.KeepAlive()
	if sink.keepAliveTimer == nil {
		sink.keepAliveTimer = time.NewTimer(time.Second)
	}
	if keepAlive != sink.KeepAliveTime {
		sink.KeepAliveTime = keepAlive
	}
}

func (sink *EventSink) sinkEvent(event SinkEvent) error {

	// check for the various capabilities passed via response events
	if keepAliver, ok := event.(KeepAliver); ok {
		sink.SetKeepAlive(keepAliver.KeepAlive())
	}

	// Handle an event ID passed along with the message
	var writeErr error
	if eventIDer, ok := event.(EventIDer); ok {
		eventID := eventIDer.EventID()
		_, writeErr = sink.w.Write(idHeader)
		if writeErr == nil {
			_, writeErr = sink.w.Write(append([]byte(eventID), newLine...))
		}
	}

	// handle a retry time passed along with a message
	if getRetryer, ok := event.(GetRetrier); ok {
		retry := resumer.GetRetry()
		if sink.RetryTime != retry {
			_, writeErr = sink.w.Write(retryHeader)
			if writeErr == nil {
				millis := []byte(strconv.Itoa(int(retry / time.Millisecond)))
				_, writeErr = sink.w.Write(append(millis, newLine...))
			}
		}
	}

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
