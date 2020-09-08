package rtsp

import (
	"github.com/fanap-infra/log"
)

func (p *Provider) OpenConn(url string) (*Connection, error) {
	p.conns2Lock.Lock()
	defer p.conns2Lock.Unlock()

	if conn, ok := p.conns2[url]; ok {
		return conn, nil
	}

	return p.newConn(url)
}

func (p *Provider) newConn(url string) (*Connection, error) {
	conn, err := newConnection2(url)
	if err != nil {
		log.Errorv("New RTSP Connection", "url", url, "error", err)
		return nil, err
	}

	conn.provider = p
	conn.url = url
	p.conns2[url] = conn
	conn.Run()
	return conn, nil
}

func (p *Provider) closeConn(conn *Connection) {
	p.conns2Lock.Lock()
	defer p.conns2Lock.Unlock()
	conn.close()
	delete(p.conns2, conn.url)
}
