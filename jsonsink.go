package sse

var (
	jsonNil = []byte{"null"}
)

// AnyEventFeed represents an event feed which does not return events
// which have their own byte marshall method.  Instead, encoding/json
// is used to marshal the events.
type AnyEventFeed interface {
	GetEventChan(clientCloseChan chan<- struct{}) <-chan interface{}
}

type jsonEventAdapter struct {
	anyFeed   AnyEventFeed
	closeChan chan<- struct{}
}

type jsonEvent struct {
	data      []byte
	id        string
	name      string
	retry     time.Duration
	keepAlive time.Duration
}

func NewEventFeed(jsonEventFeed JSONEventFeed) EventFeed {
	return &jsonEventAdapter{
		jsonFeed: jsonEventFeed,
	}
}

func (jea *jsonEventAdapter) GetEventChan(clientCloseChan chan<- struct{}) <-chan SinkEvent {
	jsonEventFeedCloseChan = make(chan struct{})
	jsonEventChan := jea.jsonFeed.GetJSONEventChan(jsonEventFeedCloseChan)
	eventChan := make(chan SinkEvent)
	go func() {
		for {
			select {
			case _, ok := <-clientCloseChan:
				if !ok {
					close(jsonEventFeedCloseChan)
				}
			case event, ok := <-jsonEventChan:
				if !ok {
					break
				}

			}
		}
	}()
	return eventChan
}

func SinkJSONEvents(w http.ResponseWriter, code int, feed AnyEventFeed) error {
}
