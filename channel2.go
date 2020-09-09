package rtsp

type Channel2 struct {
	conn *connection2
	pos  int64
}

func newChannel2(conn *connection2) *Channel2 {
	return &Channel2{
		conn: conn,
		pos:  -1,
	}
}

func (c *Channel2) ReadPacket() *Packet {
	return c.conn.ReadPacket(&c.pos)
}

func (c *Channel2) Close() {
	c.conn.doneChannel()
}
