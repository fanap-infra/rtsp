package rtsp

import (
	"bytes"
	"encoding/binary"
	"io"
	neturl "net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fanap-infra/log"
	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/client"
	rtspcodec "github.com/fanap-infra/rtsp/codec"
	"github.com/fanap-infra/rtsp/codec/h264parser"
)

type connection2 struct {
	provider *Provider
	url      string
	host     string
	rtsp     *client.Client
	onceLoop sync.Once

	h264InfoChanged bool
	h264Info        bytes.Buffer
	sps             []byte
	pps             []byte

	// wg           sync.WaitGroup
	cond         *sync.Cond
	bufLen       int64
	buf          []Packet
	bufThreshold int64
	bufIndex     int64 // TODO: max check int64 overflow

	streamRef int32
	done      chan struct{}
}

func newConnection2(url string, provider *Provider) (conn *connection2, err error) {
	host := url
	u, err := neturl.Parse(url)
	if err == nil {
		host = u.Host
	}
	log.Infov("RTSP Opening", "host", host, "url", url)

	rtsp, err := client.Dial(url)
	if err != nil {
		log.Errorv("Open RTSP Connection", "url", url, "error", err)
		return nil, err
	}

	conn = &connection2{
		rtsp:         rtsp,
		provider:     provider,
		url:          url,
		host:         host,
		cond:         sync.NewCond(&sync.Mutex{}),
		bufLen:       400,
		buf:          make([]Packet, 400),
		bufThreshold: 300,
		bufIndex:     -1,
		done:         make(chan struct{}),
		streamRef:    0,
	}

	return
}

func (c *connection2) Run() {
	c.onceLoop.Do(func() {
		codecs, err := c.rtsp.Streams()
		if err != nil {
			log.Errorv("Read RTSP Codecs", "error", err)
			return
		}
		c.setCodecs(codecs)
		go c.loop()
	})
}

func (c *connection2) setCodecs(codecs []av.CodecData) {
	for _, codec := range codecs {
		switch codec.Type() {
		case av.H264:
			h264 := codec.(h264parser.CodecData)
			c.sps = h264.SPS()
			c.pps = h264.PPS()

			_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
			_ = binary.Write(&c.h264Info, binary.BigEndian, c.sps)
			_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
			_ = binary.Write(&c.h264Info, binary.BigEndian, c.pps)
		case av.AAC:
			log.Errorf("mp4: codec type=%v is not implement", codec.Type().String())
		case av.ONVIF_METADATA:
			metadata := codec.(rtspcodec.MetadataData)
			log.Debugf("mp4: codec type=%v uri=%s", codec.Type().String(), metadata.URI())
		default:
			log.Errorf("mp4: codec type=%v is not implement", codec.Type().String())
		}
	}
}

func (c *connection2) write(data []byte, time time.Duration, isKeyFrame bool, isMetaData bool, isEOF bool) {
	p := &c.buf[(c.bufIndex+1)%c.bufLen]
	p.Data = data
	p.IsKeyFrame = isKeyFrame
	p.IsMetaData = isMetaData
	p.IsEOF = isEOF
	p.Time = time

	c.cond.L.Lock()
	c.bufIndex++
	c.cond.Broadcast()
	c.cond.L.Unlock()
}

// loop run in Goroutine
func (c *connection2) loop() {
	var buf bytes.Buffer
	// h264Info := false
	for {
		pkt, err := c.rtsp.ReadPacket()
		if err != nil {
			c.close()
			if err != io.EOF {
				log.Errorv("RTSP Read Packet", "error", err)
			}
			return
		}

		switch {
		case pkt.IsAudio:
		case pkt.IsMetadata:
			c.write(pkt.Data, pkt.Time, false, true, false)
		default:
			pktnalus, _ := h264parser.SplitNALUs(pkt.Data)
			for _, nal := range pktnalus {
				// not I-frame or P-frame
				if nal[0] != 97 && nal[0] != 101 {
					c.sps = nal
					_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
					_ = binary.Write(&c.h264Info, binary.BigEndian, c.sps)
					_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
					_ = binary.Write(&c.h264Info, binary.BigEndian, c.pps)
					c.h264InfoChanged = true
				}
			}

			if pkt.IsKeyFrame {
				c.h264InfoChanged = false
				// h264Info = true
				_ = binary.Write(&buf, binary.BigEndian, c.h264Info.Bytes())
				_ = binary.Write(&buf, binary.BigEndian, naulStartCode)
				_ = binary.Write(&buf, binary.BigEndian, pkt.Data[4:])
				c.write(buf.Bytes(), pkt.Time, true, false, false)
				buf.Reset()
			} else {
				// h264Info = false
				c.write(pkt.Data[4:], pkt.Time, false, false, false)
			}
		}

		select {
		case <-c.done:
			c.close()
			return
		default:
		}
	}
}

func (c *connection2) close() {
	c.provider.delConn(c)
	c.write(nil, time.Duration(0), false, false, true)

	log.Infov("Close RTSP Connection", "host", c.host)
	err := c.rtsp.Close()
	if err != nil {
		log.Errorv("Close RTSP Connection", "url", c.url, "error", err)
	}
}

// call from reader go routine
// needs: packaets, packetIndex, cond and packetThreshold
// TODO: reduce code between c.cond.L.Lock() block
func (c *connection2) ReadPacket(s *Stream) *Packet {
	if s.pos < 0 { // if first read, must starting with I-frame
		c.cond.L.Lock()
		ok := c.bufIndex < 0
		if ok { // if buffer is empty
			s.pos = 0
			c.cond.Wait()
		} else {
			s.pos = c.bufIndex
		}
		c.cond.L.Unlock()

		if !ok {
			for !c.buf[s.pos%c.bufLen].IsKeyFrame {
				s.pos++
				c.cond.L.Lock()
				if s.pos > c.bufIndex {
					// log.Infov("ReadPacket I-frame Wait", "id", ch.id, "pos", ch.pos, "index", c.bufIndex, "host", c.host)
					c.cond.Wait()
				}
				c.cond.L.Unlock()
			}
		}
	} else {
		s.pos++
		c.cond.L.Lock()
		index := c.bufIndex
		if index-s.pos > c.bufThreshold {
			s.pos = index - c.bufThreshold
		} else if s.pos > index {
			// log.Infov("ReadPacket Wait", "id", ch.id, "pos", ch.pos, "index", c.bufIndex, "host", c.host)
			c.cond.Wait()
		}
		c.cond.L.Unlock()
	}

	return &c.buf[s.pos%c.bufLen]
}

func (c *connection2) createStream() *Stream {
	// c.wg.Add(1)
	return newStream(c, atomic.AddInt32(&c.streamRef, 1))
}

func (c *connection2) doneStream() {
	// c.wg.Done()
	if atomic.AddInt32(&c.streamRef, -1) <= 0 {
		c.done <- struct{}{}
	}
}
