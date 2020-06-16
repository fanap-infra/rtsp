package rtsp

import (
	"bytes"
	"encoding/binary"
	"time"
)

const channelPacketsBufferCount = 30

type Channel struct {
	conn     *connection
	key      uint32
	started  bool
	packets  chan Packet
	prevTime time.Duration
}

func newChannel() *Channel {
	return &Channel{
		packets: make(chan Packet, channelPacketsBufferCount),
		// Close:  make(chan struct{}),
	}
}

func (ch *Channel) GetKey() uint32 {
	return ch.key
}

func (ch *Channel) Close() {
	//ch.onceClose.Do(func() {
	ch.conn.closeChannel(ch)
	//})
}

func (ch *Channel) Packets() <-chan Packet {
	return ch.packets
}

func (ch *Channel) timeToTs(tm time.Duration) time.Duration {
	return tm * time.Duration(90000) / time.Second
}

func (ch *Channel) genTime(t time.Duration) (ts time.Duration) {
	if ch.prevTime != 0 {
		ts = ch.timeToTs(t) - ch.prevTime
	}
	ch.prevTime = ch.timeToTs(t)
	return ts
}

func (ch *Channel) sendPacket(packet Packet, h264Info bool) {
	if ch.started {
		ch.packets <- Packet{
			IsKeyFrame: packet.IsKeyFrame,
			Time:       ch.genTime(packet.Time),
			Data:       packet.Data,
		}
		return
	}

	if packet.IsKeyFrame {
		ch.started = true
		if h264Info {
			ch.packets <- Packet{
				IsKeyFrame: packet.IsKeyFrame,
				Time:       ch.genTime(packet.Time),
				Data:       packet.Data,
			}
		} else {
			var buf bytes.Buffer
			_ = binary.Write(&buf, binary.BigEndian, ch.conn.h264Info.Bytes())
			_ = binary.Write(&buf, binary.BigEndian, naulStartCode)
			_ = binary.Write(&buf, binary.BigEndian, packet.Data)

			ch.packets <- Packet{
				IsKeyFrame: true,
				Time:       ch.genTime(packet.Time),
				Data:       buf.Bytes(),
			}
		}
	}
}

func (ch *Channel) writePacket(packet *Packet) {
	ch.packets <- *packet
}
