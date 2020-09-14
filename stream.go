package rtsp

import (
	"fmt"
)

type StreamReader interface {
	Read() *Packet
}

type Stream struct {
	conn *connection2
	id   int32
	pos  int64
	key  string
}

func newStream(conn *connection2, id int32) *Stream {
	return &Stream{
		conn: conn,
		id:   id,
		pos:  -1,
		key:  fmt.Sprintf("%s[%d]", conn.host, id),
	}
}

func (s *Stream) Read() *Packet {
	return s.conn.ReadPacket(s)
}

func (s *Stream) Close() {
	s.conn.doneStream()
}

func (s *Stream) Key() string {
	return s.key
}

func (s *Stream) Host() string {
	return s.conn.host
}
