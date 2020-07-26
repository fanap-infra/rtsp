package rtsp

import (
	"bytes"
	"encoding/binary"
	"sync"
	"time"
)

const channelPacketsBufferCount = 300

type Channel struct {
	conn      *connection
	key       uint32
	started   bool
	packets   chan Packet
	prevTime  time.Duration
	onceClose sync.Once
	sendLock  sync.Mutex
	closing   bool
}

func newChannel() *Channel {
	return &Channel{
		packets: make(chan Packet, channelPacketsBufferCount),
	}
}

func (ch *Channel) GetKey() uint32 {
	return ch.key
}

func (ch *Channel) Close() {
	ch.onceClose.Do(func() {
		{
			ch.sendLock.Lock()
			defer ch.sendLock.Unlock()
			ch.closing = true
		}

		ch.conn.closeChannel(ch)
	})
}

func (ch *Channel) Packets() <-chan Packet {
	return ch.packets
}

func (ch *Channel) sendPacket(packet Packet, h264Info bool) {
	{
		// ch.sendLock.Lock()
		// defer ch.sendLock.Unlock()

		if ch.closing {
			return
		}
	}
	ch.conn.lastFrameTime = time.Now().UnixNano()

	if ch.started && !packet.IsKeyFrame {
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
