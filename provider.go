package rtsp

import (
	"sync"
	"sync/atomic"

	"github.com/fanap-infra/log"
)

type Provider struct {
	conns    sync.Map
	connsKey uint32
}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Status() (resp string, err error) {
	return "OK", nil
}

func (p *Provider) OpenChannel(url string) (ch *Channel, err error) {
	if conn, ok := p.conns.Load(url); ok {
		ch = conn.(*connection).OpenChannel()
		return
	}

	conn, err := newConnection(url)
	if err != nil {
		log.Errorv("New RTSP Connection", "url", url, "error", err)
		return nil, err
	}

	conn.key = atomic.AddUint32(&p.connsKey, 1)
	conn.provider = p
	p.conns.Store(conn.key, p)
	ch = conn.OpenChannel()
	conn.Run()

	return
}
