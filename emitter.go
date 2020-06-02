package rtsp

import (
	"bytes"
	"io"

	"github.com/daneshvar/rtsp/av"
	"github.com/daneshvar/rtsp/client"
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
	//Reinit(sps []byte)
}

// listener: Listener Wrapper
type listener struct {
	Listener
	init bool
	eof  chan struct{}
}

type conn struct {
	rtsp      *client.Client
	codecs    []av.CodecData
	listeners []*listener
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
		init:     false,
		Listener: ln,
	}
	ln.WriteHeader(c.codecs)
	c.listeners = append(c.listeners, w)

	return w.eof, nil
}

func (e *Emitter) newConn(uri string) (c *conn, err error) {
	c = &conn{}

	if c.rtsp, err = client.Dial(uri); err != nil {
		return nil, err
	}

	log.Infof("RTSP Connected: %s\n", uri)

	c.codecs, _ = c.rtsp.Streams()

	go func(c *conn) {
		var buf bytes.Buffer

		for {
			pkt, err := c.rtsp.ReadPacket()
			if err != nil {
				for _, ln := range c.listeners {
					ln.WriteTrailer()
					ln.eof <- struct{}{}
				}

				if err == io.EOF {
					err = nil
					break
				}
				return
			}

			for _, ln := range c.listeners {
				if ln.init {
					ln.WritePacket(pkt)
				} else {
					if pkt.IsKeyFrame {
						ln.init = true
						ln.WritePacket(pkt)
					}
				}
			}
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
