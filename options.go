package sse

type ConfigOption interface {
	Apply(*SSEClient) error
}

func (wf WantFlag) Apply(ssec *SSEClient) error {
	ssec.demand(wf)
	return nil
}
