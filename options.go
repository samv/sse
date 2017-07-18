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

func ReconnectTime(minDelay time.Duration) ConfigOption {
	return &reconnectTime{minDelay}
}

func (rt reconnectTime) Apply(ssec *SSEClient) error {
	ssec.reconnectTime = rt.minDelay
	return nil
}
