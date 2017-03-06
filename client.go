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
	url *url.URL

	// current state of the client
	readyState ReadyState

	// current SSE response, if connected
	response *http.Response

	// reader that decodes the response
	reader      *StreamEventReader
	readerError error
	eventStream chan *Event

	// SSE state
	// reconnect time after losing connection
	lastEventId string // sticky

	// what messages are being sent through
	wantStd int32

	// channels for these "standard" messages
	messagesChan chan *Event // standard message channel
	opensChan    chan bool   // open/close notification channel
	errorsChan   chan *Event // error channel

	// reconnect time after losing connection
	reconnectTime time.Duration
}

// NewSSEClient creates a new client which can make a single SSE call.
func NewSSEClient() *SSEClient {
	ssec := &SSEClient{
		readyState:    Connecting,
		eventChan:     make(chan *Event, 1),
		onlineChan:    make(chan bool, 1),
		errorChan:     make(chan error, 1),
		want:          wantMessages,
		readyState:    Connecting,
		reconnectTime: 2 * time.Second,
	}
	return ssec
}

// GetStream makes a GET request and returns a channel for *all* events read
func (ssec *SSEClient) GetStream(uri string) error {
	ssec.Lock()
	defer ssec.Unlock()
	var err error
	if ssec.url, err = url.Parse(uri); err != nil {
		return errors.Wrap(err, "error parsing URL")
	}
	go ssec.notify()
	return err
}

func (ssec *SSEClient) makeRequest() (*http.Request, error) {
	ssec.Lock()
	defer ssec.Unlock()
	request, err := http.NewRequest("GET", ssec.uri, nil)
	if err != nil {
		return nil, err
	}

	request := req

	request.Header.Set("Accept", "text/event-stream")
	if ssec.lastEventId != "" {
		request.Header.Set("Last-Event-Id", ssec.lastEventId)
	}
	return req, nil
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

// URL returns the configured URL of the client
func (ssec *SSEClient) URL() *net.URL {
	ssec.Lock()
	retUrl := ssec.uri
	ssec.Unlock()
	return retUrl
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
				if ssec.reconnectTime > time.Duration(0) {
					time.Sleep(ssec.reconnectTime)
				}
				atomic.StoreInt32(&ssec.readyState, Connecting)
				time.Sleep(ssec.reconnectTime)
			} else {
				ssec.emit(event)
			}
		}
	}

	close(ssec.messagesChan)
}
