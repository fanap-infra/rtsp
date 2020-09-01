package rtsp

import (
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
		packets: make(chan Packet),
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

	if ch.started && !packet.IsKeyFrame && !IsClosed(ch.packets) {
		select {
		case ch.packets <- packet:
			return
			/*case <-time.After(15 * time.Millisecond):
			log.Errorf("Timeout")
			return*/
		}
	}

	if packet.IsKeyFrame && !IsClosed(ch.packets) {
		ch.started = true
		select {
		case ch.packets <- packet:
			return
			/*case <-time.After(15 * time.Millisecond):
			log.Errorf("Timeout")
			return*/
		}
	}
	return
}
func IsClosed(ch <-chan Packet) bool {
	select {
	case _, ok := <-ch:
		if ok {
			return false
		}
		return true
	default:
	}
	return false
}
