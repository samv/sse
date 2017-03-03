package sse

type Option interface {
	apply(*SSEClient)
}

// optionFunc wraps a func so it satisfies the Option interface.
type optionFunc func(*SSEClient)

func (f optionFunc) apply(ssec *SSEClient) {
	f(ssec)
}

// ReconnectTime allows the default reconnection time of 2 seconds to
// be overridden.
func ReconnectTime(duration time.Duration) Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.SetReconnectTime(duration)
	})
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

// WantMessages tells the client to always send messages, which are
// normally only sent once the client knows that someone is reading
// from the Messages() channel
func WantMessages() Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.Messages()
	})
}

// WantErrors tells the client to always send errors, instead of the
// default behavior which is to only write errors if the error channel
// is writable
func WantErrors() Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.Errors()
	})
}

// WantOpens tells the client to always send open/close events,
// instead of the default behavior which is to only write these events
// if the channel is writable
func WantOpens() Option {
	return optionFunc(func(ssec *SSEClient) {
		ssec.Opens()
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
