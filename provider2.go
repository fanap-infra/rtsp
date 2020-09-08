package rtsp

import (
	"sync/atomic"

	"github.com/fanap-infra/log"
)

func (p *Provider) GetConn(url string) (*Connection, error) {
	p.conns2Lock.Lock()
	defer p.conns2Lock.Unlock()
	if conn, ok := p.conns2[url]; ok {
		atomic.AddInt64(&conn.listenerRef, 1)
		return conn, nil
	}
	return p.newConn(url)
}

func (p *Provider) PutConn(conn *Connection) {
	if atomic.AddInt64(&conn.listenerRef, -1) <= 0 {
		conn.closeSignal <- struct{}{}
	}
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

func (p *Provider) deleteConn(conn *Connection) {
	p.conns2Lock.Lock()
	defer p.conns2Lock.Unlock()
	conn.close()
	delete(p.conns2, conn.url)
}
