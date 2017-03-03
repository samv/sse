package sse

import (
	"net/url"
	"time"
)

// ReadyState is an enum representing the current status of the connection
type ReadyState int8

const (
	Connecting ReadyState = 0
	Open       ReadyState = 1
	Closed     ReadyState = 2
)

// EventSource is a rough re-interpretation of the "EventSource" API
// in a go context, implementing Server-Sent-Events.
type EventSource interface {

	// a channel of server-sent "message" events"
	Messages() <-chan *Event

	// online/offline state notification (true = online)
	Opens() <-chan bool

	// Channel for server-sent and client-generated errors
	Errors() <-chan *Event

	// hangs up a connection
	Close()

	// the URL of the connection (or an error if the URL was bad)
	URL() (*net.URL, error)

	// access the current state of the connection
	ReadyState() ReadyState

	// access the current negotiated retry delay
	ReconnectionTime() time.Duration

	// the last 'id' value sent by the server, if any
	LastEventId() string
}

// NewEventSource is the constructor for an SSE EventSource.  It will
// initiate a connection to the URL given.
func NewEventSource(url string, options ...Option) (EventSource, error) {
	client := NewSSEClient()
	for _, option := range options {
		if err := option.apply(client); err != nil {
			return nil, err
		}
	}
	if client.shouldReconnect() {
		if _, err := client.GetStream(url); err != nil {
			return nil, err
		}
	} else if !client.url {
		if err := client.SetStreamRequest("GET", url); err != nil {
			return nil, err
		}
	}
	return client
}
