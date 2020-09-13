package rtsp

import (
	"github.com/fanap-infra/log"
)

func (p *Provider) OpenStream(url string) (s *Stream, err error) {
	var conn *connection2
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

func (p *Provider) newConn(url string) (conn *connection2, err error) {
	conn, err = newConnection2(url, p)
	if err != nil {
		log.Errorv("New RTSP Connection", "url", url, "error", err)
		return
	}
	p.conns2[url] = conn
	conn.Run()
	return
}

func (p *Provider) delConn(conn *connection2) {
	log.Debugv("RTSP Provider delete connection", "url", conn.url)
	p.conns2Lock.Lock()
	defer p.conns2Lock.Unlock()
	delete(p.conns2, conn.url)
}
