package rtsp

import (
	"sync"

	"github.com/fanap-infra/log"
)

type Provider struct {
	conns sync.Map

	// ver2
	conns2     map[string]*connection
	conns2Lock sync.Mutex
}

func NewProvider() *Provider {
	return &Provider{
		conns2: make(map[string]*connection),
	}
}

func (p *Provider) Status() (resp string, err error) {
	return "OK", nil
}

func (p *Provider) OpenStream(url string) (s *Stream, err error) {
	var conn *connection
	ok := false

	{
		p.conns2Lock.Lock()
		defer p.conns2Lock.Unlock()

		if conn, ok = p.conns2[url]; !ok {
			if conn, err = p.newConn(url); err != nil {
				return
			}
		}
	}

	return conn.createStream(), nil
}

func (p *Provider) newConn(url string) (conn *connection, err error) {
	conn, err = newConnection(url, p)
	if err != nil {
		log.Errorv("New RTSP Connection", "url", url, "error", err)
		return
	}
	p.conns2[url] = conn
	conn.Run()
	return
}

func (p *Provider) delConn(conn *connection) {
	log.Debugv("RTSP Provider delete connection", "url", conn.url)
	p.conns2Lock.Lock()
	defer p.conns2Lock.Unlock()
	delete(p.conns2, conn.url)
}
