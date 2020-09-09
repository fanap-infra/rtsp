package rtsp

import (
	"time"
)

type PacketReader interface {
	ReadPacket() *Packet
}

type Packet struct {
	IsMetaData bool
	IsKeyFrame bool
	IsEOF      bool
	Time       time.Duration // packet decode time
	Data       []byte
	Seq        uint64
	Hash       [16]byte
}
