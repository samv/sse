package sse

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/pkg/errors"
	//"golang.org/x/net/context"
)

type ReadyState int32

const (
	// bitwise flags for selecting which events to pass through
	wantErrors    = 1
	wantOpenClose = 2
	wantMessages  = 4

	// ReadyState is an enum representing the current status of the connection
	Connecting ReadyState = 0
	Open       ReadyState = 1
	Closed     ReadyState = 2
)

// SSEClient is a wrapper for an http Client which can also hold one
// SSE session open, and implements EventSource.  You can use this API
// directly, but the EventSource API will steer you towards message
// protocols and behavior which also work from browsers.
type SSEClient struct {
	http.Client

	// all state changes are guarded by this
	sync.Mutex

	// info of the request.  The SSE spec only allows GET requests and no body!
	url     *url.URL
	verb    string
	body    io.ReadSeeker
	headers http.Header

	// current state of the client
	readyState ReadyState

	// current SSE response, if connected
	response *http.Response

	// reader that decodes the response
	reader      *StreamEventReader
	readerError error
	eventStream chan *Event

	// SSE state
	origin string // pre-packaged origin value for events
	// reconnect time after losing connection
	lastEventId string // sticky
	// what messages are being sent through
	// 1. "standard" channels
	wantStd      int32
	messagesChan chan *Event // standard message channel
	opensChan    chan bool   // open/close notification channel
	errorsChan   chan *Event // error channel

	// 2. explicitly subscribed events
	wantTypes map[string]chan<- *Event

	// whether to reconnect and after what time
	reconnect     bool
	reconnectTime time.Duration

	// facilities for canceling clients
	ctx       context.Context
	transport *http.Transport
}

// NewSSEClient creates a new client which can make a single SSE call.
func NewSSEClient() *SSEClient {
	ssec := &SSEClient{
		eventChan:     make(chan *Event, 1),
		onlineChan:    make(chan bool, 1),
		errorChan:     make(chan error, 1),
		want:          wantMessages,
		readyState:    Connecting,
		reconnectTime: 2 * time.Second,
	}
	ssec.transport = &http.Transport{}
	ssec.Client.Transport = ssec.transport
	return ssec
}

// SetMessagesBufferSize lets you specify how much buffering you want.
// The default is one event.
func (ssec *SSEClient) SetMessagesBufferSize(size int) {
	ssec.messagesChan = make(chan *Event, size)
}

func (ssec *SSEClient) SetOpensBufferSize(size int) {
	ssec.opensChan = make(chan bool, size)
}

func (ssec *SSEClient) SetErrorsBufferSize(size int) {
	ssec.errorsChan = make(chan *Event, size)
}

// SetContext allows the context to be specified - this affects
// cancelation and timeouts.  Affects active client on reconnection only.
func (ssec *SSEClient) SetContext(ctx context.Context) {
	ssec.Lock()
	ssec.ctx = ctx
	ssec.Unlock()
}

// GetStream makes a GET request and returns a channel for *all* events read
func (ssec *SSEClient) GetStream(uri string) (<-chan Event, error) {
	if err := ssec.SetStreamRequest("GET", uri, nil, nil); err != nil {
		return nil, err
	}
	return ssec.stream()
}

// PostStream makes a POST request and returns a channel for *all*
// events read.  Note: it is not possible to do this in browsers
// without special re-implementations of EventSource and IE polyfill
func (ssec *SSEClient) PostStream(uri string, body io.ReadSeeker, headers http.Header) (<-chan Event, error) {
	if err := ssec.SetStreamRequest("POST", uri, body, headers); err != nil {
		return nil, err
	}
	return ssec.stream()
}

// SetStreamRequest allows the request for the next reconnection to be
// altered
func (ssec *SSEClient) SetStreamRequest(method, uri string, body io.ReadSeeker, headers http.Header) error {
	ssec.Lock()
	defer ssec.Unlock()
	ssec.verb = method
	ssec.body = body
	ssec.headers = headers
	var err error
	ssec.url, err = url.Parse(uri)
	if err == nil && ssec.baseURL != nil {
		ssec.url = ssec.baseURL.ResolveReference(ssec.url)
	}
	return err
}

func (ssec *SSEClient) makeRequest() (*http.Request, error) {
	ssec.Lock()
	defer ssec.Unlock()
	request, err := http.NewRequest(ssec.verb, ssec.uri, ssec.body)
	if err != nil {
		return nil, err
	}

	// go 1.7+ presumably
	var cancelCtx context.Context
	cancelCtx, ssec.cancel = context.WithCancel(context.Background())
	request := req.WithContext(cancelCtx)

	for header, vals := range ssec.headers {
		request.Header[header] = vals
	}
	return nil
	request.Header.Set("Accept", "text/event-stream")
	if ssec.lastEventId != "" {
		request.Header.Set("Last-Event-Id", ssec.lastEventId)
	}
	return request, nil
}

func (ssec *SSEClient) connect() error {
	var err error
	if err = ssec.makeRequest(); err != nil {
		return err
	}

	ssec.response, err = ssec.Client.Do(ssec.request)
	ssec.origin = makeOrigin(ssec.response.Request.URL)
	switch ssec.response.StatusCode {
	case http.StatusOK:
		var typeOk bool
		var contentType string
		// FIXME - this is not RFC-compliant
		for _, mimeType := range ssec.response.Header["Content-Type"] {
			contentType = mimeType
			if strings.Index(mimeType, "text/event-stream") >= 0 {
				typeOk = true
				break
			}
		}
		if typeOk {
			return nil
		} else {
			// HTTP 200 OK responses that have a Content-Type other
			// than text/event-stream (or some other supported type)
			// must cause the user agent to fail the connection.
			err = errors.Errorf("Content type not text/event-stream: %s", contentType)
		}
	case http.StatusNoContent, http.StatusResetContent:
		// HTTP 204 No Content, and 205 Reset Content responses are
		// equivalent to 200 OK responses with the right MIME type but
		// no content, and thus must reset the connection.
		return nil
	case http.StatusMovedPermanently, http.StatusPermanentRedirect:
		// TODO - update the URL, origin
	case http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect:
		// TODO - retry different URL but don't update URL.  Update origin?
	case http.StatusUseProxy:
		// TODO
	case http.StatusUnauthorized, http.StatusProxyAuthRequired:
		// TODO
	default:
		err = errors.Errorf("Bad response: %s", ssec.response.Status)
	}

	// TODO: reset the connection here if error

	return err
}

func (ssec *SSEClient) setReconnect(should bool) {
	ssec.Lock()
	ssec.reconnect = false
	ssec.Unlock()
}

func (ssec *SSEClient) Close() {
	ssec.setReconnect(false)
	// TODO - this approach to canceling a request is deprecated, but
	// the new method is go 1.7+; needs conditional compilation
	ssec.transport.CancelRequest(ssec.request)
}

// Reopen allows a connection which was closed to be re-opened again.
func (ssec *SSEClient) Reopen() error {
	ssec.setReconnect(true)
	_, err := ssec.stream()
	return err
}

func (ssec *SSEClient) shouldReconnect() bool {
	ssec.Lock()
	should := ssec.reconnect
	ssec.Unlock()
	return should
}

func (ssec *SSEClient) want(what int32) {
	if (atomic.LoadInt32(&ssec.wantStd) & what) == 0 {
		// seems silly to use both atomic load and mutex, but this is
		// a read, modify, update, and the fastpath is lockless atomic read
		ssec.Lock()
		wants := atomic.LoadInt32(&ssec.wantStd) | what
		atomic.StoreInt32(&ssec.wantStd, what)
		ssec.Unlock()
	}
}

func (ssec *SSEClient) addEventChannel(eventType string, eventChan chan<- *Event) {
	ssec.Lock()
	ssec.wantTypes[messageType] = eventChan
	ssec.Unlock()
}
func (ssec *SSEClient) Messages() <-chan *Event {
	ssec.want(wantMessages)
	return ssec.messagesChan
}

func (ssec *SSEClient) emit(event *Event) {
	// only send "message" events down the Messages() channel
	switch event.Type {
	case MessageType:
		if (atomic.LoadInt32(&ssec.wantStd) & wantMessages) == 0 {
			select {
			case ssec.messagesChan <- event:
			default:
			}
		} else {
			ssec.messagesChan <- event
		}
	case ErrorType:
		if (atomic.LoadInt32(&ssec.wantStd) & wantErrors) == 0 {
			select {
			case ssec.errorsChan <- event:
			default:
			}
		} else {
			ssec.errorsChan <- event
		}
	}
	// always send to explicitly configured channels
	if sendChan := ssec.getEmitChan(event.Type); sendChan != nil {
		sendChan <- event
	}
}

func (ssec *SSEClient) getEmitChan(eventType string) <-chan *Event {
	ssec.Lock()
	rchan := ssec.wantTypes[eventType]
	ssec.Unlock()
	return rchan
}

func (ssec *SSEClient) Opens() <-chan bool {
	ssec.want(wantOpens)
	return ssec.opensChan
}

func (ssec *SSEClient) emitOpenClose(which bool) {
	if (atomic.LoadInt32(&ssec.wantStd) & wantOpens) == 0 {
		select {
		case ssec.opensChan <- which:
		default:
		}
	} else {
		ssec.opensChan <- which
	}
}

func (ssec *SSEClient) Errors() <-chan *Event {
	ssec.want(wantErrors)
	return ssec.errorsChan
}

func (ssec *SSEClient) emitError(err error) {
	packagedError := &Event{
		Origin:      ssec.origin, // not entirely true - might be the wrong thing
		Error:       err,
		Type:        ErrorType,
		LastEventId: ssec.lastEventId,
	}
	ssec.emit(packagedError)
}

// URL returns the configured URL of the client, which may have an error
func (ssec *SSEClient) URL() (*net.URL, error) {
	ssec.Lock()
	defer ssec.Unlock()
	return url.Parse(ssec.uri)
}

func (ssec *SSEClient) readStream() {
	ssec.eventChan = make(chan *Event)
	ssec.reader = NewStreamEventReader(ssec.response.Body)
	ssec.readerError = reader.decode(eventChan)
	close(eventChan)
}

func (ssec *SSEClient) process() {
	for {
		if atomic.LoadInt32(&ssec.readyState) == Connecting {
			err := ssec.connect()
			if err != nil {
				ssec.StoreInt32(&ssec.readyState, Closed)
				ssec.emitError(err)
				break
			} else {
				atomic.StoreInt32(&ssec.readyState, Open)
			}
		}
		go readStream()
		select {
		case event, ok := <-eventChan:
			if !ok {
				ssec.response.Body.Close()
				ssec.response = nil
				ssec.emitOpenClose(false)
				if reader.Retry >= time.Duration(0) {
					ssec.reconnectTime = reader.Retry
				}
				if ssec.reconnectTime > time.Duration(0) {
					time.Sleep(ssec.reconnectTime)
				}
				if ssec.shouldReconnect() {
					atomic.StoreInt32(&ssec.readyState, Connecting)
					if ssec.reconnectTime > time.Duration(0) {
						time.Sleep(ssec.reconnectTime)
					}
				}
			} else {
				ssec.emit(event)
			}
		}
	}

	close(ssec.messagesChan)
}
