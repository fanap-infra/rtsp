package rtsp

import (
	"bytes"
	"encoding/binary"
)

const channelPacketsBufferCount = 30

type Channel struct {
	conn    *connection
	key     uint32
	started bool
	packets chan Packet
	//onceClose sync.Once
	//prevTime uint32
	// Close   chan<- struct{}
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

func (ch *Channel) sendPacket(packet Packet, h264Info bool) {
	if ch.started {
		ch.packets <- packet
		return
	}

	if packet.IsKeyFrame {
		ch.started = true
		if h264Info {
			ch.packets <- packet
		} else {
			var buf bytes.Buffer
			_ = binary.Write(&buf, binary.BigEndian, ch.conn.h264Info.Bytes())
			_ = binary.Write(&buf, binary.BigEndian, naulStartCode)
			_ = binary.Write(&buf, binary.BigEndian, packet.Data)

			ch.packets <- Packet{
				IsKeyFrame: true,
				Time:       packet.Time,
				Data:       buf.Bytes(),
			}
		}
	}
}
