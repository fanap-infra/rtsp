package rtsp

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"

	"github.com/fanap-infra/log"
	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/client"
)

// Emitter send RTSP packet to listeners
type Emitter struct {
	conns map[string]*conn
}

// Listener interface
type Listener interface {
	WriteHeader(codecs []av.CodecData) (further bool)
	WritePacket(pkt av.Packet) (further bool)
	WriteTrailer()
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
	listeners   sync.Map
	listenersID uint32
}

func (c *conn) closeListener(ln *listener, id interface{}) {
	ln.WriteTrailer()
	ln.eof <- struct{}{}
	c.listeners.Delete(id)

	log.Debugv("RTSP Close Listener", "id", id)
}

func (c *conn) writePacket(ln *listener, pkt av.Packet, id interface{}) {
	if !ln.WritePacket(pkt) {
		c.closeListener(ln, id)
	}
}

func (c *conn) run() {
	var buf bytes.Buffer
	// log.Infov("RTSP Start Read Packet", "url", uri)
	for {
		pkt, err := c.rtsp.ReadPacket()
		//log.Debugv("RTSP Read Packet", "time", pkt.Time, "key", pkt.IsKeyFrame) // "index", pkt.Idx, "cotime", pkt.CompositionTime

		if err != nil {
			c.listeners.Range(func(key, value interface{}) bool {
				c.closeListener(value.(*listener), key)
				return true
			})

			if err != io.EOF {
				log.Errorv("RTSP Read Packet", "error", err)
			}
			return
		}

		c.listeners.Range(func(key, value interface{}) bool {
			ln := value.(*listener)

			go func(ln *listener, pkt av.Packet, id interface{}) {
				if ln.started {
					c.writePacket(ln, pkt, key)
				} else {
					if pkt.IsKeyFrame {
						ln.started = true
						c.writePacket(ln, pkt, key)
					}
				}
			}(ln, pkt, key)

			return true
		})

		buf.Reset()
	}
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

	if ln.WriteHeader(c.codecs) {
		w := &listener{
			started:  false,
			Listener: ln,
		}
		id := atomic.AddUint32(&c.listenersID, 1)
		c.listeners.Store(id, w)
		log.Debugv("RTSP Add Listener", "id", id)
		return w.eof, nil
	}

	return nil, nil
}

func (e *Emitter) newConn(uri string) (c *conn, err error) {
	c = &conn{}

	if c.rtsp, err = client.Dial(uri); err != nil {
		return nil, err
	}

	log.Infov("RTSP New Connected", "url", uri)
	c.codecs, _ = c.rtsp.Streams()
	go c.run()
	return c, nil
}

// NewEmitter create a new
func NewEmitter() *Emitter {
	return &Emitter{
		conns: make(map[string]*conn),
	}
}
