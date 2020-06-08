package client

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fanap-infra/rtsp/av"
)

func (self *Client) handleRtpPacket(block []byte) (pkt av.Packet, ok bool, err error) {
	_, chnlIdentifier, _ := self.parseRtpTCPHeader(block)
	if chnlIdentifier%2 != 0 {
		if DebugRtp {
			fmt.Println("rtsp: rtcp block len", len(block)-4)
		}
		return
	}

	i := chnlIdentifier / 2
	if int(i) >= len(self.streams) {
		err = fmt.Errorf("rtsp: block no=%d invalid", chnlIdentifier)
		return
	}
	stream := self.streams[i]

	herr := stream.handleRtpPacket(block[4:])
	if herr != nil {
		if !SkipErrRtpBlock {
			err = herr
			return
		}
	}

	// if stream.gotpkt {
	/*
		TODO: sync AV by rtcp NTP timestamp
		TODO: handle timestamp overflow
		https://tools.ietf.org/html/rfc3550
		A receiver can then synchronize presentation of the audio and video packets by relating
		their RTP timestamps using the timestamp pairs in RTCP SR packets.
	*/
	// if stream.firsttimestamp == 0 {
	// 	stream.firsttimestamp = stream.timestamp
	// }
	// stream.timestamp -= stream.firsttimestamp

	ok = true
	pkt = stream.pkt
	pkt.Time = time.Duration(stream.timestamp) * time.Second / time.Duration(stream.timeScale())
	// pkt.Idx = int8(self.setupMap[i])

	if pkt.Time < stream.lasttime || pkt.Time-stream.lasttime > time.Minute*30 {
		err = fmt.Errorf("rtp: time invalid stream# time=%v lasttime=%v", pkt.Time, stream.lasttime)
		return
	}
	stream.lasttime = pkt.Time

	// if DebugRtp {
	// 	fmt.Println("rtp: pktout", pkt.Idx, pkt.Time, len(pkt.Data))
	// }

	stream.pkt = av.Packet{}
	// 	stream.gotpkt = false
	// }

	return
}

/*
https://www.ietf.org/rfc/rfc2326.txt


10.12 Embedded (Interleaved) Binary Data
   Stream data such as RTP packets is encapsulated by an ASCII dollar
   sign (24 hexadecimal), followed by a one-byte channel identifier,
   followed by the length of the encapsulated binary data as a binary,
   two-byte integer in network byte order. The stream data follows
   immediately afterwards, without a CRLF, but including the upper-layer
   protocol headers. Each $ block contains exactly one upper-layer
   protocol data unit, e.g., one RTP packet.

   The channel identifier is defined in the Transport header with the
   interleaved parameter(Section 12.39).

S->C: $\000{2 byte length}{"length" bytes data, w/RTP header}

The logic for channelIdentifier is defined in Setup() function which multiply number of
channels by 2 and

	odd Channel num -> RTSP(Controlling)
	even Channel num -> RTP(Packets)


RTP: rtp header look like this.


		0                   1                   2                   3
		0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|V=2|P|X|  CC   |M|     PT      |       sequence number         |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|                           timestamp                           |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|           synchronization source (SSRC) identifier            |
		+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+
		|            contributing source (CSRC) identifiers             |
		|                             ....                              |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+




*/
func (c *Client) parseRtpTCPHeader(h []byte) (uint16, uint8, bool) {
	channelIdentifier := uint8(h[1])
	length := binary.BigEndian.Uint16(h[2:4])
	if int(channelIdentifier/2) >= len(c.streams) {
		return 0, 0, false
	}

	//RTSP Channel
	if channelIdentifier%2 == 1 {
		return 0, 0, false
	}

	//Small rtp Packet
	if length < 8 {
		return 0, 0, false
	}

	// V=2
	// first two bit 0xc0 = 11000000
	if h[4]&0xc0 != 0x80 {
		return 0, 0, false
	}

	stream := c.streams[channelIdentifier/2]

	//Stream type same as packet
	//PT = payload type
	// last seven bit 0x7f=01111111
	if int(h[5]&0x7f) != stream.Sdp.PayloadType {
		return 0, 0, false
	}

	// timestamp := binary.BigEndian.Uint32(h[8:12])

	// if stream.firsttimestamp == 0 { //TODO: can we remove firsttimestamp
	// 	return length, channelIdentifier, true
	// }

	//check if time stamp is too old or too early
	//TODO: BUG: what if we were too early (stream.timeScale()*60*60)
	// timestamp -= stream.firsttimestamp
	// if timestamp < stream.timestamp {
	// 	return 0, 0, false
	// } else if timestamp-stream.timestamp > uint32(stream.timeScale()*60*60) {
	// 	return 0, 0, false
	// }

	return length, channelIdentifier, true
}

func (self *Stream) handleRtpPacket(packet []byte) (err error) {
	if self.isCodecDataChange() {
		err = errCodecDataChange
		return
	}

	if self.client != nil && DebugRtp {
		fmt.Println("rtp: packet", self.CodecData.Type(), "len", len(packet))
		dumpsize := len(packet)
		if dumpsize > 32 {
			dumpsize = 32
		}
		fmt.Print(hex.Dump(packet[:dumpsize]))
	}

	/*
		0                   1                   2                   3
		0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|V=2|P|X|  CC   |M|     PT      |       sequence number         |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|                           timestamp                           |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		|           synchronization source (SSRC) identifier            |
		+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+=+
		|            contributing source (CSRC) identifiers             |
		|                             ....                              |
		+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
	*/
	if len(packet) < 8 {
		err = fmt.Errorf("rtp: packet too short")
		return
	}
	payloadOffset := 12 + int(packet[0]&0xf)*4
	if payloadOffset > len(packet) {
		err = fmt.Errorf("rtp: packet too short")
		return
	}
	timestamp := binary.BigEndian.Uint32(packet[4:8])
	payload := packet[payloadOffset:]

	/*
		PT 	Encoding Name 	Audio/Video (A/V) 	Clock Rate (Hz) 	Channels 	Reference
		0	PCMU	A	8000	1	[RFC3551]
		1	Reserved
		2	Reserved
		3	GSM	A	8000	1	[RFC3551]
		4	G723	A	8000	1	[Vineet_Kumar][RFC3551]
		5	DVI4	A	8000	1	[RFC3551]
		6	DVI4	A	16000	1	[RFC3551]
		7	LPC	A	8000	1	[RFC3551]
		8	PCMA	A	8000	1	[RFC3551]
		9	G722	A	8000	1	[RFC3551]
		10	L16	A	44100	2	[RFC3551]
		11	L16	A	44100	1	[RFC3551]
		12	QCELP	A	8000	1	[RFC3551]
		13	CN	A	8000	1	[RFC3389]
		14	MPA	A	90000		[RFC3551][RFC2250]
		15	G728	A	8000	1	[RFC3551]
		16	DVI4	A	11025	1	[Joseph_Di_Pol]
		17	DVI4	A	22050	1	[Joseph_Di_Pol]
		18	G729	A	8000	1	[RFC3551]
		19	Reserved	A
		20	Unassigned	A
		21	Unassigned	A
		22	Unassigned	A
		23	Unassigned	A
		24	Unassigned	V
		25	CelB	V	90000		[RFC2029]
		26	JPEG	V	90000		[RFC2435]
		27	Unassigned	V
		28	nv	V	90000		[RFC3551]
		29	Unassigned	V
		30	Unassigned	V
		31	H261	V	90000		[RFC4587]
		32	MPV	V	90000		[RFC2250]
		33	MP2T	AV	90000		[RFC2250]
		34	H263	V	90000		[Chunrong_Zhu]
		35-71	Unassigned	?
		72-76	Reserved for RTCP conflict avoidance				[RFC3551]
		77-95	Unassigned	?
		96-127	dynamic	?			[RFC3551]
	*/
	//payloadType := packet[1]&0x7f

	switch self.Sdp.Type {
	case av.H264:
		if err = self.handleH264Payload(timestamp, payload); err != nil {
			return
		}

	case av.AAC:
		if len(payload) < 4 {
			err = fmt.Errorf("rtp: aac packet too short")
			return
		}
		payload = payload[4:] // TODO: remove this hack
		// self.gotpkt = true
		self.pkt.Data = payload
		self.timestamp = timestamp

	default:
		// self.gotpkt = true
		self.pkt.Data = payload
		self.timestamp = timestamp
	}

	return
}
