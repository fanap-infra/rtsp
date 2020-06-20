package rtspcon

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"runtime/trace"
	"time"

	"github.com/pion/rtp"
)

//RtpCon handle rtp connection to camera.
type RtpCon struct {
	reader        *bufio.Reader
	sleepDuration time.Duration
}

func (conn *RtpCon) parseTCPRtp() (*rtp.Packet, error) {
	rtpPkt := rtp.Packet{}
	if _, err := conn.reader.ReadBytes('$'); err != nil {
		return nil, err
	}
	header := make([]byte, 3)
	if _, err := io.ReadFull(conn.reader, header); err != nil {
		return nil, err
	}
	if header[0] != uint8(0) {
		return nil, errors.New("Not h264 channel")
	}
	length := binary.BigEndian.Uint16(header[1:3])
	rawRtp := make([]byte, length)
	n, err := io.ReadFull(conn.reader, rawRtp)
	if err != nil {
		return nil, err
	}
	if uint16(n) != length {
		return nil, errors.New("Lenght of packet and read doesnt match")
	}
	if err := rtpPkt.Unmarshal(rawRtp); err != nil {
		return nil, err
	}
	return &rtpPkt, nil
}

//ReadTCPPacket read rtp packet from tcp connection.
func (conn *RtpCon) ReadTCPPacket() chan *rtp.Packet {
	ch := make(chan *rtp.Packet, 100)
	go func() {
		defer trace.StartRegion(context.Background(), "myTracedRegion").End()
		// conn.reader.Peek(4096)
		for {
			// conn.reader.Peek(4096)
			// for i := 0; i < 15; i++ {
				pkt, err := conn.parseTCPRtp()
				if err != nil {
					continue
				}
				ch <- pkt
			// }
			time.Sleep(conn.sleepDuration)
		}

	}()
	return ch
}
