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

const naulStartCode = uint32(0x00_00_00_01)

type connection struct {
	provider *Provider
	url      string
	host     string
	rtsp     *client.Client
	onceLoop sync.Once

	// h264InfoChanged bool
	h264Info bytes.Buffer
	//sps []byte
	//pps []byte

	// wg           sync.WaitGroup
	lock         sync.RWMutex
	cond         *sync.Cond
	bufLen       int64
	buf          []Packet
	bufThreshold int64
	bufIndex     int64 // TODO: max check int64 overflow

	streamRef int32
	done      chan struct{}
}

func newConnection(url string, provider *Provider) (conn *connection, err error) {
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

	conn = &connection{
		rtsp:         rtsp,
		provider:     provider,
		url:          url,
		host:         host,
		bufLen:       1000,
		buf:          make([]Packet, 1000),
		bufThreshold: 300,
		bufIndex:     -1,
		done:         make(chan struct{}),
		streamRef:    0,
	}

	conn.cond = sync.NewCond(conn.lock.RLocker())
	return
}

func (c *connection) Run() {
	c.onceLoop.Do(func() {
		go c.loop()
	})
}

func (c *connection) readCodecs() (pps []byte, err error) {
	codecs, err := c.rtsp.Streams()
	if err != nil {
		log.Errorv("Read RTSP Codecs", "error", err)
		return nil, err
	}

	for _, codec := range codecs {
		switch codec.Type() {
		case av.H264:
			h264 := codec.(h264parser.CodecData)
			sps := h264.SPS()
			pps = h264.PPS()

			_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
			_ = binary.Write(&c.h264Info, binary.BigEndian, sps)
			_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
			_ = binary.Write(&c.h264Info, binary.BigEndian, pps)
		case av.AAC:
			log.Errorf("mp4: codec type=%v is not implement", codec.Type().String())
		case av.ONVIF_METADATA:
			metadata := codec.(rtspcodec.MetadataData)
			log.Debugf("mp4: codec type=%v uri=%s", codec.Type().String(), metadata.URI())
		default:
			log.Errorf("mp4: codec type=%v is not implement", codec.Type().String())
		}
	}

	return
}

// loop run in Goroutine
func (c *connection) loop() {
	pps, err := c.readCodecs()
	if err != nil {
		log.Errorv("Read RTSP Codecs", "error", err)
		return
	}

	var buf bytes.Buffer
	// h264InfoChanged := true
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
					c.h264Info.Reset()
					_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
					_ = binary.Write(&c.h264Info, binary.BigEndian, nal) // sps
					_ = binary.Write(&c.h264Info, binary.BigEndian, naulStartCode)
					_ = binary.Write(&c.h264Info, binary.BigEndian, pps)
					// h264InfoChanged = true
				}
			}

			if pkt.IsKeyFrame /*&& h264InfoChanged*/ {
				// h264InfoChanged = false
				buf.Reset()
				_ = binary.Write(&buf, binary.BigEndian, c.h264Info.Bytes())
				_ = binary.Write(&buf, binary.BigEndian, naulStartCode)
				_ = binary.Write(&buf, binary.BigEndian, pkt.Data[4:])
				c.write(buf.Bytes(), pkt.Time, true, false, false)
			} else {
				c.write(pkt.Data[4:], pkt.Time, pkt.IsKeyFrame, false, false)
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

func (c *connection) close() {
	c.provider.delConn(c)
	c.write(nil, time.Duration(0), false, false, true)

	log.Infov("Close RTSP Connection", "host", c.host)
	err := c.rtsp.Close()
	if err != nil {
		log.Errorv("Close RTSP Connection", "url", c.url, "error", err)
	}
}

func (c *connection) write(data []byte, time time.Duration, isKeyFrame bool, isMetaData bool, isEOF bool) {
	p := &c.buf[(c.bufIndex+1)%c.bufLen]
	p.Data = data
	p.IsKeyFrame = isKeyFrame
	p.IsMetaData = isMetaData
	p.IsEOF = isEOF
	p.Time = time

	// c.lock.Lock()
	// defer c.lock.Unlock()
	//c.bufIndex++
	atomic.AddInt64(&c.bufIndex, 1)

	//c.bufIndex++
	//if c.bufIndex > 300 {
	c.cond.Broadcast()
	//}

}

// call from reader go routine
// needs: packaets, packetIndex, cond and packetThreshold
func (c *connection) ReadPacket(s *Stream) *Packet {
	if s.pos < 0 { // if first read, must starting with I-frame
		ok := c.bufIndex < 0
		if ok { // if buffer is empty
			s.pos = 0
			c.wait()
		} else {
			s.pos = c.bufIndex
		}

		if !ok {
			for !c.buf[s.pos%c.bufLen].IsKeyFrame {
				s.pos++
				if s.pos > c.bufIndex {
					// log.Infov("ReadPacket I-frame Wait", "key", s.key, "pos", s.pos, "index", c.bufIndex)
					c.wait()
				}
			}
		}
	} else {
		s.pos++
		if c.bufIndex-s.pos > c.bufThreshold {
			s.pos = c.bufIndex - c.bufThreshold
			// skip to I-frame
			for !c.buf[s.pos%c.bufLen].IsKeyFrame {
				s.pos++
				if s.pos > c.bufIndex {
					// log.Infov("ReadPacket I-frame Wait", "key", s.key, "pos", s.pos, "index", c.bufIndex)
					c.wait()
				}
			}

		} else if s.pos > c.bufIndex {
			// log.Infov("ReadPacket Wait", "key", s.key, "pos", s.pos, "index", c.bufIndex)
			c.wait()
		}
	}

	return &c.buf[s.pos%c.bufLen]
}

func (c *connection) wait() {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()
	c.cond.Wait()
}

func (c *connection) createStream() *Stream {
	// c.wg.Add(1)
	return newStream(c, atomic.AddInt32(&c.streamRef, 1))
}

func (c *connection) doneStream() {
	// c.wg.Done()
	if atomic.AddInt32(&c.streamRef, -1) <= 0 {
		c.done <- struct{}{}
	}
}
