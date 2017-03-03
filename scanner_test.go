package sse

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidUTF8(t *testing.T) {
	assert.Equal(t, []byte("normal"), validUTF8([]byte("normal")), "ascii")
	assert.Equal(t, []byte("valíd"), validUTF8([]byte("valíd")), "valid")
	assert.Equal(t, []byte("inval\uFFFD"), validUTF8(append([]byte("inval"), '\xFF')), "invalid")
}

var testCases = [][]string{
	[]string{""},
	[]string{"\uFEFF"},
	[]string{"\uFEFFid", "id"},
	[]string{"﹅: wat\nwat:wat", "﹅", ": ", "wat", "\n", "wat", ": ", "wat", "\n"},
	[]string{"\uFEF3: hi\nid: foo\ndata:\n",
		"\uFEF3", ": ", "hi", "\n", "id", ": ", "foo", "\n", "data", ": ", "\n"},
	[]string{"data:foo\rdata:baz\r\ndata:blah\r",
		"data", ": ", "foo", "\n",
		"data", ": ", "baz", "\n",
		"data", ": ", "blah", "\n"},
	[]string{string(append([]byte("data: hello"), '\x80', '\n')),
		"data", ": ", "hello\uFFFD", "\n"},
	[]string{string(append([]byte("data:"), '\xa0', '\xa1', '\n')),
		"data", ": ", "\uFFFD", "\n"},
	[]string{":test\r\rid:\rid\r",
		":", "test", "\n", "\n", "id", ": ", "\n", "id", "\n"},
	[]string{"data:", "data", ": "},
}

func TestScannerHappy(t *testing.T) {
	for testNum, testCase := range testCases {
		testIs := func(x string, args ...interface{}) string {
			return fmt.Sprintf("test case %d: "+x, append([]interface{}{testNum + 1},
				args...)...)
		}
		scanner := bufio.NewScanner(strings.NewReader(testCase[0]))
		scanner.Split(SplitFunc())
		idx := 1
		for scanner.Scan() {
			if idx >= len(testCase) {
				assert.Equal(t, "\n", scanner.Text(),
					testIs("no extra tokens, except allowable EndOfLine's"))
			} else {
				assert.Equal(t, testCase[idx], scanner.Text(), testIs("scanned token %d is correct", idx))
			}
			idx++
		}
		if idx < len(testCase) {
			assert.Equal(t, len(testCase)-1, idx-1, testIs("no missing tokens"))
		}
		assert.NoError(t, scanner.Err(), testIs("no error raised"))
	}
}

type slowReader struct {
	max int
	buf io.Reader
}

func (sr *slowReader) Read(p []byte) (n int, err error) {
	if len(p) > sr.max {
		p = p[0:sr.max]
	}
	return sr.buf.Read(p)
}

// TestScannerSlowly tests that short reads are parsed the same as full reads.
func TestScannerSlowly(t *testing.T) {
	for testNum, testCase := range testCases {
		testIs := func(x string, args ...interface{}) string {
			return fmt.Sprintf("test case %d: "+x, append([]interface{}{testNum + 1},
				args...)...)
		}
		reader := strings.NewReader(testCase[0])
		scanner := bufio.NewScanner(&slowReader{max: 1, buf: reader})
		scanner.Split(SplitFunc())
		idx := 1
		for scanner.Scan() {
			if idx >= len(testCase) {
				assert.Equal(t, "\n", scanner.Text(),
					testIs("no extra tokens, except allowable EndOfLine's"))
			} else {
				assert.Equal(t, testCase[idx], scanner.Text(), testIs("scanned token %d is correct", idx))
			}
			idx++
		}
		if idx < len(testCase) {
			assert.Equal(t, len(testCase)-1, idx-1, testIs("no missing tokens"))
		}
		assert.NoError(t, scanner.Err(), testIs("no error raised"))
	}
}
