package sse

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type streamTestCase struct {
	stream   []byte
	expected []*Event
}

func assertEmitsEvents(t *testing.T, expected []*Event, stream []byte, groupLabel string) *eventStreamReader {
	reader := bytes.NewReader(stream)
	dummyOrigin := "http://test.example.com/"
	protocol := newEventStreamReader(reader, dummyOrigin)

	idx := 0
	for yielded := range protocol.decodeChan() {
		eventNum := fmt.Sprintf("%s, event #%d", groupLabel, idx+1)
		if assert.NotNil(t, yielded, eventNum) {
			if assert.True(t, idx < len(expected), eventNum+" (no extra events)") {
				assert.Equal(t, dummyOrigin, yielded.Origin, eventNum)
				expected := expected[idx]
				assert.Equal(t, string(expected.Data), string(yielded.Data), eventNum)
				assert.Equal(t, expected.Type, yielded.Type, eventNum)
				assert.Equal(t, expected.EventID, yielded.EventID, eventNum)
			}
		}
		idx++
	}
	assert.Equal(t, len(expected), idx, groupLabel+": correct # of events generated")

	return protocol
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
			{Type: "message", EventID: "1", Data: []byte("first event")},
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
		assertEmitsEvents(t, eg.expected, eg.stream, fmt.Sprintf("Spec Example #%d", i+1))
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
		assertEmitsEvents(t, eg.expected, eg.stream, fmt.Sprintf("Custom Event Test #%d", i+1))
	}
}

func TestRetry(t *testing.T) {
	// eg from https://www.html5rocks.com/en/tutorials/eventsource/basics/
	retryTest := streamTestCase{
		stream:   []byte("retry: 10000\ndata: hello world\n\n"),
		expected: []*Event{{Type: "message", Data: []byte(`hello world`)}},
	}
	protocol := assertEmitsEvents(t, retryTest.expected, retryTest.stream, "retry test")
	assert.Equal(t, 10*time.Second, protocol.Retry)
}
