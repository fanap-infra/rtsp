package rtsp

type StreamReader interface {
	Read() *Packet
}

type Stream struct {
	conn *connection2
	id   int32
	pos  int64
}

func newStream(conn *connection2, id int32) *Stream {
	return &Stream{
		conn: conn,
		id:   id,
		pos:  -1,
	}
}

func (s *Stream) Read() *Packet {
	return s.conn.ReadPacket(s)
}

func (s *Stream) Close() {
	s.conn.doneStream()
}
