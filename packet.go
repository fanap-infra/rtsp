package rtsp

import (
	"time"
)

type Packet struct {
	IsMetaData bool
	IsKeyFrame bool
	Time       time.Duration // packet decode time
	Data       []byte
	//Seq        uint64
	//Hash       [16]byte
}
