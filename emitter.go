package rtsp

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"

	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/client"
	"gitlab.com/behnama2/log"
)

// Emitter send RTSP packet to listeners
type Emitter struct {
	conns map[string]*conn
}

// Listener interface
type Listener interface {
	WriteHeader(codecs []av.CodecData) (err error)
	WritePacket(pkt av.Packet) (err error)
	WriteTrailer() (err error)
}

// listener: Listener Wrapper
type listener struct {
	Listener
	// id      uint32
	started bool
	eof     chan struct{}
}

type conn struct {
	rtsp   *client.Client
	codecs []av.CodecData
	// listeners []*listener
	listeners  sync.Map
	listenerID uint32
}

func (e *Emitter) getConn(rtspURL string) (*conn, error) {
	conn, ok := e.conns[rtspURL]
	if !ok {
		var err error
		if conn, err = e.newConn(rtspURL); err != nil {
			return nil, err
		}
		e.conns[rtspURL] = conn
	}

	return conn, nil
}

// AddListener add new writer
func (e *Emitter) AddListener(rtspURL string, ln Listener) (chan struct{}, error) {
	c, err := e.getConn(rtspURL)
	if err != nil {
		return nil, err
	}

	w := &listener{
		// id:       id,
		started:  false,
		Listener: ln,
	}

	ln.WriteHeader(c.codecs)
	id := atomic.AddUint32(&c.listenerID, 1)
	c.listeners.Store(id, w)
	return w.eof, nil
}

func (e *Emitter) newConn(uri string) (c *conn, err error) {
	c = &conn{}

	if c.rtsp, err = client.Dial(uri); err != nil {
		return nil, err
	}

	log.Infov("RTSP New Connected", "url", uri)

	c.codecs, _ = c.rtsp.Streams()

	go func(c *conn) {
		var buf bytes.Buffer
		log.Infov("RTSP Start Read Packet", "url", uri)
		for {
			pkt, err := c.rtsp.ReadPacket()
			if err != nil {
				c.listeners.Range(func(key, value interface{}) bool {
					ln := value.(*listener)
					ln.WriteTrailer()
					ln.eof <- struct{}{}
					c.listeners.Delete(key)
					return true
				})

				if err != io.EOF {
					log.Errorv("RTSP Read Packet", "error", err)
				}
				return
			}

			c.listeners.Range(func(_, value interface{}) bool {
				ln := value.(*listener)
				if ln.started {
					ln.WritePacket(pkt)
				} else {
					if pkt.IsKeyFrame {
						ln.started = true
						ln.WritePacket(pkt)
					}
				}
				return true
			})

			buf.Reset()
		}
	}(c)

	return c, nil
}

// NewEmitter create a new
func NewEmitter() *Emitter {
	return &Emitter{
		conns: make(map[string]*conn),
	}
}
