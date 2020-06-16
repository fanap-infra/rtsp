package rtsp

import (
	"sync"

	"github.com/fanap-infra/log"
)

type Provider struct {
	conns sync.Map
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

	conn.provider = p
	conn.url = url
	p.conns.Store(url, conn)
	ch = conn.OpenChannel()
	conn.Run()

	return
}
