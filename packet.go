package rtsp

import (
	"time"
)

type Packet struct {
	IsKeyFrame bool
	Time       time.Duration // packet decode time
	Data       []byte
}
