package rtsp

import (
	"bytes"
	"encoding/binary"
	"io"
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

	listenerRef int64
	closeSignal chan struct{}
}

func newConnection2(url string, provider *Provider) (conn *connection2, err error) {
	log.Debugv("RTSP Opening Connection", "url", url)
	rtsp, err := client.Dial(url)
	if err != nil {
		log.Errorv("Open RTSP Connection", "url", url, "error", err)
		return nil, err
	}

	conn = &connection2{
		rtsp: rtsp,
		// streamStartTime: time.Now(),
		provider:     provider,
		url:          url,
		cond:         sync.NewCond(&sync.Mutex{}),
		bufLen:       400,
		buf:          make([]Packet, 400),
		bufThreshold: 300,
		bufIndex:     -1,
		closeSignal:  make(chan struct{}),
		listenerRef:  0,
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
			log.Errorf("mp4: codec type=%v uri=%s", codec.Type().String(), metadata.URI())
		default:
			log.Errorf("mp4: codec type=%v is not implement", codec.Type().String())
		}
	}
}

func (c *connection2) write(data []byte, time time.Duration, isKeyFrame bool, isMetaData bool, isEOF bool) {
	c.cond.L.Lock()

	c.bufIndex++
	p := &c.buf[c.bufIndex%c.bufLen]

	p.Data = data
	p.IsKeyFrame = isKeyFrame
	p.IsMetaData = isMetaData
	p.IsEOF = isEOF
	p.Time = time

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

		if pkt.IsAudio {
			// TODO: handle audio, so that it can be played.
			continue
		}

		if pkt.IsMetadata {
			// s := string(pkt.Data)
			// if strings.Contains(s, `Name="IsMotion" Value="true"`) {
			// 	log.Infov("ONVIF_METADATA", "IsMotion", true)
			// } else if strings.Contains(s, `Name="IsMotion" Value="false"`) {
			// 	log.Infov("ONVIF_METADATA", "IsMotion", false)
			// }
			// fmt.Println(s)
			c.write(pkt.Data, pkt.Time, false, true, false)
			continue
		}

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

		select {
		case <-c.closeSignal:
			c.close()
			return
		default:
		}
	}
}

func (c *connection2) close() {
	c.provider.delConn(c)

	c.write(nil, time.Duration(0), false, false, true)

	log.Debugv("Close RTSP Connection", "url", c.url)
	err := c.rtsp.Close()
	if err != nil {
		log.Errorv("Close RTSP Connection", "url", c.url, "error", err)
	}
}

// call from reader go routine
// needs: packaets, packetIndex, cond and packetThreshold
// TODO: reduce code between c.cond.L.Lock() block
func (c *connection2) ReadPacket(pos *int64) *Packet {
	c.cond.L.Lock()

	switch {
	case c.bufIndex < 0: // if buffer is empty
		*pos = 0
		c.cond.Wait()
	case *pos < 0: // if first read
		*pos = c.bufIndex
	case c.bufIndex-*pos > c.bufThreshold:
		*pos = c.bufIndex - c.bufThreshold
	default:
		*pos++
		if *pos > c.bufIndex {
			c.cond.Wait()
		}
	}

	p := &c.buf[*pos%c.bufLen]
	c.cond.L.Unlock()
	return p
}

func (c *connection2) addChannel() *Channel2 {
	atomic.AddInt64(&c.listenerRef, 1)
	// c.wg.Add(1)
	return newChannel2(c)
}

func (c *connection2) doneChannel() {
	// c.wg.Done()
	if atomic.AddInt64(&c.listenerRef, -1) <= 0 {
		c.closeSignal <- struct{}{}
	}
}
