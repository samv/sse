package sse

import (
	"time"
)

type ConfigOption interface {
	Apply(*SSEClient) error
}

func (wf WantFlag) Apply(ssec *SSEClient) error {
	ssec.demand(wf)
	return nil
}

type reconnectTime struct {
	minDelay time.Duration
}

// ReconnectTime allows the default reconnection time of 2 seconds to
// be overridden.
func ReconnectTime(minDelay time.Duration) ConfigOption {
	return &reconnectTime{minDelay}
}

func (rt reconnectTime) Apply(ssec *SSEClient) error {
	ssec.reconnectTime = rt.minDelay
	return nil
}

// BaseReference allows the base reference for the URL to be passed in,
// and the main one may be relative
func BaseReference(uri string) Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.SetBaseURL(uri)
	})
}

// WithContext allows a context to be passed down, allowing
// cancelation and operation timeouts.
func WithContext(ctx context.Context) Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.setContext(ctx)
	})
}

// NoAutoConnect tells the client not to connect when first created;
// it will instead auto-connect once Messages() is called
func NoAutoConnect() Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.setReconnect(false)
	})
}

// AddEventChannel is similar to addEventListener in JavaScript's
// EventSource.  It is the way that you subscribe to any event which
// is not type "message" or "error".  Without these subscriptions,
// there is no way to receive non-standard messages.  A nil channel
// indicates to send them all via Messages()
func AddEventChannel(messageType string, eventChan chan<- *Event) Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.addEventChannel(messageType, eventChan)
	})
}

// Buffering allows you to override the default channel buffer depths for
// a connection.
func Buffered(messagesChanSize, opensChanSize, errorsChanSize int) Option {
	return optionFunc(func(ssec *SSEClient) {
		if messagesChanSize != 1 {
			ssec.SetMessagesBufferSize(messagesChanSize)
		}
		if opensChanSize != 1 {
			ssec.SetOpensBufferSize(opensChanSize)
		}
		if errorsChanSize != 1 {
			ssec.SetErrorsBufferSize(errorsChanSize)
		}
	})
}
