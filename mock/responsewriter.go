package mock

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
)

// ResponseWriter implements net/http.ResponseWriter but buffers all the writes
// and provides a method to return the response object
type ResponseWriter struct {
	header          http.Header
	buffer          []byte
	written         []byte
	code            int
	writeError      error
	closed          bool
	closeNotifyChan chan bool
}

// NewResponseWriter returns a new mock response writer object.
func NewResponseWriter() *ResponseWriter {
	return &ResponseWriter{
		header:          make(http.Header),
		closeNotifyChan: make(chan bool),
	}
}

// Header returns the mock header; this does not emulate all of the
// magic that net/http's responsewriter does and does not try to
// convert headers set let to trailers
func (w *ResponseWriter) Header() http.Header {
	return w.header
}

// Write emulates a chunk of data sent to the response
func (w *ResponseWriter) Write(data []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.writeError != nil {
		return -1, w.writeError
	}
	w.buffer = append(w.buffer, data...)
	return len(data), nil
}

// Flush moves buffered data to the response, satisfying http.Flusher
func (w *ResponseWriter) Flush() {
	w.written = append(w.written, w.buffer...)
	w.buffer = nil
}

// WriteHeader records a header as sent
func (w *ResponseWriter) WriteHeader(code int) {
	w.code = code
}

// CloseNotify records a header as sent, satisfying http.CloseNotifier
func (w *ResponseWriter) CloseNotify() <-chan bool {
	return w.closeNotifyChan
}

// Close closes the notify channel
func (w *ResponseWriter) Close() {
	if !w.closed {
		w.closed = true
		w.closeNotifyChan <- w.closed
	}
}

// GetResponse returns a minimal synthetic response based on the
// response that was written
func (w *ResponseWriter) GetResponse() *http.Response {
	w.Flush()
	return &http.Response{
		Status:     fmt.Sprintf("%.3d Meh", w.code),
		StatusCode: w.code,
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		ProtoMinor: 0,
		Request:    &http.Request{Method: "GET"},
		Header:     w.header,
		Body:       ioutil.NopCloser(bytes.NewReader(w.written)),
	}
}

// ResponseBytes returns the response so far
func (w *ResponseWriter) ResponseBytes() []byte {
	return w.written
}
