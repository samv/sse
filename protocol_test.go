package sse

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type streamTestCase struct {
	stream   []byte
	expected []*Event
}

func AssertEmitsEvents(t *testing.T, expected []*Event, stream []byte, groupLabel string) (*EventStreamReader, error) {
	reader := bytes.NewReader(stream)
	dummyOrigin := "http://test.example.com/"
	protocol := NewEventStreamReader(reader, dummyOrigin)

	eventChan := make(chan *Event)
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	var protocolError error
	go func() {
		protocolError = protocol.decode(eventChan)
		close(eventChan)
		waitGroup.Done()
	}()

	idx := 0
	for yielded := range eventChan {
		eventNum := fmt.Sprintf("%s, event #%d", groupLabel, idx+1)
		if assert.NotNil(t, yielded, eventNum) {
			if assert.True(t, idx < len(expected), eventNum+" (no extra events)") {
				assert.Equal(t, dummyOrigin, yielded.Origin, eventNum)
				expected := expected[idx]
				assert.Equal(t, string(expected.Data), string(yielded.Data), eventNum)
				assert.Equal(t, expected.Type, yielded.Type, eventNum)
				assert.Equal(t, expected.LastEventId, yielded.LastEventId, eventNum)
			}
		}
		idx++
	}
	assert.Equal(t, len(expected), idx, groupLabel+": correct # of events generated")

	waitGroup.Wait()
	assert.NoError(t, protocolError, groupLabel+": no error")
	return protocol, protocolError
}

var specExampleStreams = []streamTestCase{
	{
		stream: []byte(
			`data: YHOO
data: +2
data: 10

`),
		expected: []*Event{
			{Type: "message", Data: []byte("YHOO\n+2\n10")},
		},
	},
	{
		stream: []byte(
			`: test stream

data: first event
id: 1

data: second event
id

data: third event`),
		expected: []*Event{
			{Type: "message", LastEventId: "1", Data: []byte("first event")},
			{Type: "message", Data: []byte("second event")},
			{Type: "message", Data: []byte("third event")},
		},
	},
	{
		stream: []byte(
			`data

data
data

data:
`),
		expected: []*Event{
			{Type: "message", Data: []byte{}},
		},
	},
	{
		stream: []byte(
			`data:test

data: test
`),
		expected: []*Event{
			{Type: "message", Data: []byte("test")},
			{Type: "message", Data: []byte("test")},
		},
	},
}

func TestSpecExamples(t *testing.T) {
	for i, eg := range specExampleStreams {
		AssertEmitsEvents(t, eg.expected, eg.stream, fmt.Sprintf("Spec Example #%d", i+1))
	}
}

var customEventTests = []streamTestCase{
	{ // eg from https://www.html5rocks.com/en/tutorials/eventsource/basics/
		stream: []byte(
			`data: {"msg": "First message"}

event: userlogon
data: {"username": "John123"}

event: update
data: {"username": "John123", "emotion": "happy"}
`),
		expected: []*Event{
			{Type: "message", Data: []byte(`{"msg": "First message"}`)},
			{Type: "userlogon", Data: []byte(`{"username": "John123"}`)},
			{Type: "update", Data: []byte(`{"username": "John123", "emotion": "happy"}`)},
		},
	},
}

func TestCustomEvents(t *testing.T) {
	for i, eg := range customEventTests {
		AssertEmitsEvents(t, eg.expected, eg.stream, fmt.Sprintf("Custom Event Test #%d", i+1))
	}
}

func TestRetry(t *testing.T) {
	// eg from https://www.html5rocks.com/en/tutorials/eventsource/basics/
	retryTest := streamTestCase{
		stream:   []byte("retry: 10000\ndata: hello world\n\n"),
		expected: []*Event{{Type: "message", Data: []byte(`hello world`)}},
	}
	protocol, _ := AssertEmitsEvents(t, retryTest.expected, retryTest.stream, "retry test")
	assert.Equal(t, 10*time.Second, protocol.Retry)
}
