package rtsp

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"

	"github.com/fanap-infra/log"
	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/client"
	rtspcodec "github.com/fanap-infra/rtsp/codec"
	"github.com/fanap-infra/rtsp/codec/h264parser"
)

const naulStartCode = uint32(0x00_00_00_01)

type connection struct {
	provider *Provider

	url         string
	chans       []Channel
	rtsp        *client.Client
	channels    sync.Map
	channelsKey uint32

	wg sync.WaitGroup

	h264InfoChanged bool
	h264Info        bytes.Buffer
	sps             []byte
	pps             []byte
}

func newConnection(url string) (conn *connection, err error) {
	log.Debugv("RTSP Opening Connection", "url", url)
	rtsp, err := client.Dial(url)
	if err != nil {
		log.Errorv("Open RTSP Connection", "url", url, "error", err)
		return nil, err
	}

	conn = &connection{
		rtsp: rtsp,
	}

	return
}

func (c *connection) OpenChannel() *Channel {
	ch := newChannel()
	ch.conn = c
	atomic.AddUint32(&c.channelsKey, 1)
	ch.key = c.channelsKey
	log.Debugv("New Channel", "key", ch.key)
	c.channels.Store(ch.key, ch)
	c.wg.Add(1)

	return ch
}

func (c *connection) Run() {
	codecs, err := c.rtsp.Streams()
	if err != nil {
		log.Errorv("Read RTSP Codecs", "error", err)
		return
	}
	c.setCodecs(codecs)
	go c.loop()
}

func (c *connection) loop() {
	var buf bytes.Buffer
	var packet Packet
	h264Info := false

	for {
		pkt, err := c.rtsp.ReadPacket()

		if err != nil {
			c.channels.Range(func(_, value interface{}) bool {
				c.closeChannel(value.(*Channel))
				return true
			})

			if err != io.EOF {
				log.Errorv("RTSP Read Packet", "error", err)
			}
			return
		}

		if pkt.IsMetadata {
			// s := string(pkt.Data)
			// if strings.Contains(s, `Name="IsMotion" Value="true"`) {
			// 	log.Infov("ONVIF_METADATA", "IsMotion", true)
			// } else if strings.Contains(s, `Name="IsMotion" Value="false"`) {
			// 	log.Infov("ONVIF_METADATA", "IsMotion", false)
			// }
			// fmt.Println(s)
			packet.IsMetaData = true
			packet.IsKeyFrame = false
			packet.Time = pkt.Time
			packet.Data = pkt.Data
			empty := true
			c.channels.Range(func(_, value interface{}) bool {
				empty = false
				if value.(*Channel).started {
					go value.(*Channel).sendPacket(packet, h264Info)
				}
				return true
			})
			if empty {
				break
			}
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

		if pkt.IsKeyFrame && c.h264InfoChanged {
			c.h264InfoChanged = false
			h264Info = true
			_ = binary.Write(&buf, binary.BigEndian, c.h264Info.Bytes())
			_ = binary.Write(&buf, binary.BigEndian, naulStartCode)
			_ = binary.Write(&buf, binary.BigEndian, pkt.Data[4:])

			packet.IsKeyFrame = pkt.IsKeyFrame
			packet.Time = pkt.Time
			packet.Data = buf.Bytes()
		} else {
			h264Info = false
			packet.IsKeyFrame = pkt.IsKeyFrame
			packet.IsMetaData = false
			packet.Time = pkt.Time
			packet.Data = pkt.Data[4:]
		}

		// ToDo: this way is bad for check empty Channel
		empty := true
		c.channels.Range(func(_, value interface{}) bool {
			empty = false
			if value.(*Channel).started || pkt.IsKeyFrame {
				go value.(*Channel).sendPacket(packet, h264Info)
			}
			return true
		})
		buf.Reset()
		if empty {
			break
		}
	}
	c.wg.Wait()
	c.close()
}

func (c *connection) close() {
	log.Debugv("Close RTSP Connection", "url", c.url)
	c.provider.conns.Delete(c.url)
	err := c.rtsp.Close()
	if err != nil {
		log.Errorv("Close RTSP Connection", "url", c.url, "error", err)
	}
}

func (c *connection) closeChannel(ch *Channel) {
	c.channels.Delete(ch.key)
	close(ch.packets)
	c.wg.Done()
	log.Debugv("Close RTSP Channel", "key", ch.key)
}

func (c *connection) setCodecs(codecs []av.CodecData) {
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
