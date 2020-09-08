package rtsp

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
	"time"

	"github.com/fanap-infra/log"
	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/client"
	rtspcodec "github.com/fanap-infra/rtsp/codec"
	"github.com/fanap-infra/rtsp/codec/h264parser"
)

type Connection struct {
	provider *Provider
	url      string
	rtsp     *client.Client
	onceLoop sync.Once

	h264InfoChanged bool
	h264Info        bytes.Buffer
	sps             []byte
	pps             []byte

	wg           sync.WaitGroup
	cond         *sync.Cond
	bufLen       int64
	buf          []Packet
	bufThreshold int64
	bufIndex     int64

	listenerRef int64
	closeSignal chan struct{}
}

func newConnection2(url string) (conn *Connection, err error) {
	log.Debugv("RTSP Opening Connection", "url", url)
	rtsp, err := client.Dial(url)
	if err != nil {
		log.Errorv("Open RTSP Connection", "url", url, "error", err)
		return nil, err
	}

	conn = &Connection{
		rtsp: rtsp,
		// streamStartTime: time.Now(),
		cond:         sync.NewCond(&sync.Mutex{}),
		bufLen:       400,
		buf:          make([]Packet, 400),
		bufThreshold: 300,
		bufIndex:     -1,
		closeSignal:  make(chan struct{}),
	}

	return
}

func (c *Connection) Run() {
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

func (c *Connection) setCodecs(codecs []av.CodecData) {
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

func (c *Connection) write(data []byte, time time.Duration, isKeyFrame bool, isMetaData bool) {
	c.cond.L.Lock()

	c.bufIndex++
	p := &c.buf[c.bufIndex%c.bufLen]

	p.Data = data
	p.IsKeyFrame = isKeyFrame
	p.IsMetaData = isMetaData
	p.Time = time

	c.cond.Broadcast()
	c.cond.L.Unlock()
}

// loop run in Goroutine
func (c *Connection) loop() {
	var buf bytes.Buffer
	// h264Info := false
	for {
		pkt, err := c.rtsp.ReadPacket()
		if err != nil {
			// c.write(pkt.Data, pkt.Time, false, true) end
			if err != io.EOF {
				log.Errorv("RTSP Read Packet", "error", err)
			}
			break
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
			c.write(pkt.Data, pkt.Time, false, true)
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
			c.write(buf.Bytes(), pkt.Time, true, false)
			buf.Reset()
		} else {
			// h264Info = false
			c.write(pkt.Data[4:], pkt.Time, false, false)
		}

		select {
		case <-c.closeSignal:
			break
		default:
		}
	}

	c.wg.Wait()
	c.provider.deleteConn(c)
}

func (c *Connection) close() {
	log.Debugv("Close RTSP Connection", "url", c.url)
	err := c.rtsp.Close()
	if err != nil {
		log.Errorv("Close RTSP Connection", "url", c.url, "error", err)
	}
}

// call from reader go routine
// needs: packaets, packetIndex, cond and packetThreshold
func (c *Connection) GetPacket(pos *int64) *Packet {
	c.cond.L.Lock()
	if c.bufIndex-*pos > c.bufThreshold {
		*pos = c.bufIndex - c.bufThreshold
	} else {
		*pos++
	}
	if *pos > c.bufIndex {
		c.cond.Wait()
	}
	p := &c.buf[*pos%c.bufLen]
	c.cond.L.Unlock()
	return p
}
