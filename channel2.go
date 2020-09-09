package rtsp

type Channel2 struct {
	conn *connection2
	id   int32
	pos  int64
}

func newChannel2(conn *connection2, id int32) *Channel2 {
	return &Channel2{
		conn: conn,
		id:   id,
		pos:  -1,
	}
}

func (c *Channel2) ReadPacket() *Packet {
	return c.conn.ReadPacket(c)
}

func (c *Channel2) Close() {
	c.conn.doneChannel()
}
