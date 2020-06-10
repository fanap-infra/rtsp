package client

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/codec"
	"github.com/fanap-infra/rtsp/codec/aacparser"
	"github.com/fanap-infra/rtsp/codec/h264parser"
	"github.com/fanap-infra/rtsp/sdp"
	"github.com/fanap-infra/rtsp/utils/bits/pio"
	"github.com/pion/rtp"
	"gitlab.com/behnama2/log"
)

type Stream struct {
	av.CodecData
	Sdp    sdp.Media
	client *Client

	// h264
	fuStarted  bool
	fuBuffer   []byte
	sps        []byte
	pps        []byte
	spsChanged bool
	ppsChanged bool

	gotpkt         bool
	pkt            av.Packet
	timestamp      uint32
	firsttimestamp uint32

	lasttime time.Duration
}

func (self *Stream) clearCodecDataChange() {
	self.spsChanged = false
	self.ppsChanged = false
}

func (self *Stream) isCodecDataChange() bool {
	if self.spsChanged && self.ppsChanged {
		return true
	}
	return false
}

func (self *Stream) timeScale() int {
	t := self.Sdp.TimeScale
	if t == 0 {
		// https://tools.ietf.org/html/rfc5391
		t = 8000
	}
	return t
}

func (self *Stream) makeCodecData() (err error) {
	media := self.Sdp

	// daneshvar.ho PayloadType -> 35-71 Unassigned
	if (media.PayloadType >= 96 && media.PayloadType <= 127) || (media.PayloadType >= 35 || media.PayloadType <= 71) {
		switch media.Type {
		case av.H264:
			for _, nalu := range media.SpropParameterSets {
				if len(nalu) > 0 {
					self.handleH264Payload(0, nalu)
				}
			}

			if len(self.sps) == 0 || len(self.pps) == 0 {
				if nalus, typ := h264parser.SplitNALUs(media.Config); typ != h264parser.NALU_RAW {
					for _, nalu := range nalus {
						if len(nalu) > 0 {
							self.handleH264Payload(0, nalu)
						}
					}
				}
			}

			if len(self.sps) > 0 && len(self.pps) > 0 {
				if self.CodecData, err = h264parser.NewCodecDataFromSPSAndPPS(self.sps, self.pps); err != nil {
					err = fmt.Errorf("rtsp: h264 sps/pps invalid: %s", err)
					return
				}
			} else {
				err = fmt.Errorf("rtsp: missing h264 sps or pps")
				return
			}

		case av.AAC:
			if len(media.Config) == 0 {
				err = fmt.Errorf("rtsp: aac sdp config missing")
				return
			}
			if self.CodecData, err = aacparser.NewCodecDataFromMPEG4AudioConfigBytes(media.Config); err != nil {
				err = fmt.Errorf("rtsp: aac sdp config invalid: %s", err)
				return
			}
		}
	} else {
		switch media.PayloadType {
		case 0:
			self.CodecData = codec.NewPCMMulawCodecData()

		case 8:
			self.CodecData = codec.NewPCMAlawCodecData()

		default:
			err = fmt.Errorf("rtsp: PayloadType=%d unsupported", media.PayloadType)
			return
		}
	}

	return
}

func (self *Stream) handleBuggyAnnexbH264Packet(timestamp uint32, packet []byte) (isBuggy bool, err error) {
	if len(packet) >= 4 && packet[0] == 0 && packet[1] == 0 && packet[2] == 0 && packet[3] == 1 {
		isBuggy = true
		if nalus, typ := h264parser.SplitNALUs(packet); typ != h264parser.NALU_RAW {
			for _, nalu := range nalus {
				if len(nalu) > 0 {
					if err = self.handleH264Payload(timestamp, nalu); err != nil {
						return
					}
				}
			}
		}
	}
	return
}

func (self *Stream) handleH264Payload(timestamp uint32, packet []byte) (err error) {
	if len(packet) < 2 {
		err = fmt.Errorf("rtp: h264 packet too short")
		return
	}

	var isBuggy bool
	if isBuggy, err = self.handleBuggyAnnexbH264Packet(timestamp, packet); isBuggy {
		return
	}

	naluType := packet[0] & 0x1f

	/*
		Table 7-1 – NAL unit type codes
		1   ￼Coded slice of a non-IDR picture
		5    Coded slice of an IDR picture
		6    Supplemental enhancement information (SEI)
		7    Sequence parameter set
		8    Picture parameter set
		1-23     NAL unit  Single NAL unit packet             5.6
		24       STAP-A    Single-time aggregation packet     5.7.1
		25       STAP-B    Single-time aggregation packet     5.7.1
		26       MTAP16    Multi-time aggregation packet      5.7.2
		27       MTAP24    Multi-time aggregation packet      5.7.2
		28       FU-A      Fragmentation unit                 5.8
		29       FU-B      Fragmentation unit                 5.8
		30-31    reserved                                     -
	*/
	switch {
	case naluType >= 1 && naluType <= 5:
		if naluType == 5 {
			self.pkt.IsKeyFrame = true
		}
		self.gotpkt = true
		// raw nalu to avcc
		b := make([]byte, 4+len(packet))
		pio.PutU32BE(b[0:4], uint32(len(packet)))
		copy(b[4:], packet)
		self.pkt.Data = b
		self.timestamp = timestamp

	case naluType == 7: // sps
		// daneshvar.ho
		// h := packet
		// info, err := h264parser.ParseSPS(h)
		// if err != nil {
		// 	log.Errorc(err.Error())
		// } else {
		// 	log.Infov("SPS", "width", info.Width, "height", info.Height)
		// }
		// daneshvar.ho

		if self.client != nil && DebugRtp {
			fmt.Println("rtsp: got sps")
		}
		if len(self.sps) == 0 {
			self.sps = packet
			self.makeCodecData()
			log.Info("RTSP: makeCodecData")
		} else if bytes.Compare(self.sps, packet) != 0 {
			self.spsChanged = true
			self.sps = packet
			log.Info("RTSP: SPS changed")

			// daneshvar.ho
			self.pkt.IsKeyFrame = false
			self.gotpkt = true
			self.pkt.Data = self.sps
			self.timestamp = timestamp
			// daneshvar.ho

			if self.client != nil && DebugRtp {
				fmt.Println("rtsp: sps changed")
			}
		}

	case naluType == 8: // pps
		if self.client != nil && DebugRtp {
			fmt.Println("rtsp: got pps")
		}
		if len(self.pps) == 0 {
			self.pps = packet
			self.makeCodecData()
		} else if bytes.Compare(self.pps, packet) != 0 {
			self.ppsChanged = true
			self.pps = packet
			if self.client != nil && DebugRtp {
				fmt.Println("rtsp: pps changed")
			}
		}

	case naluType == 28: // FU-A
		/*
			0                   1                   2                   3
			0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			| FU indicator  |   FU header   |                               |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+                               |
			|                                                               |
			|                         FU payload                            |
			|                                                               |
			|                               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                               :...OPTIONAL RTP padding        |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			Figure 14.  RTP payload format for FU-A

			The FU indicator octet has the following format:
			+---------------+
			|0|1|2|3|4|5|6|7|
			+-+-+-+-+-+-+-+-+
			|F|NRI|  Type   |
			+---------------+


			The FU header has the following format:
			+---------------+
			|0|1|2|3|4|5|6|7|
			+-+-+-+-+-+-+-+-+
			|S|E|R|  Type   |
			+---------------+

			S: 1 bit
			When set to one, the Start bit indicates the start of a fragmented
			NAL unit.  When the following FU payload is not the start of a
			fragmented NAL unit payload, the Start bit is set to zero.

			E: 1 bit
			When set to one, the End bit indicates the end of a fragmented NAL
			unit, i.e., the last byte of the payload is also the last byte of
			the fragmented NAL unit.  When the following FU payload is not the
			last fragment of a fragmented NAL unit, the End bit is set to
			zero.

			R: 1 bit
			The Reserved bit MUST be equal to 0 and MUST be ignored by the
			receiver.

			Type: 5 bits
			The NAL unit payload type as defined in table 7-1 of [1].
		*/
		fuIndicator := packet[0]
		fuHeader := packet[1]
		isStart := fuHeader&0x80 != 0
		isEnd := fuHeader&0x40 != 0
		if isStart {
			self.fuStarted = true
			self.fuBuffer = []byte{fuIndicator&0xe0 | fuHeader&0x1f}
		}
		if self.fuStarted {
			self.fuBuffer = append(self.fuBuffer, packet[2:]...)
			if isEnd {
				self.fuStarted = false
				if err = self.handleH264Payload(timestamp, self.fuBuffer); err != nil {
					return
				}
			}
		}

	case naluType == 24: // STAP-A
		/*
			0                   1                   2                   3
			0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                          RTP Header                           |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|STAP-A NAL HDR |         NALU 1 Size           | NALU 1 HDR    |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                         NALU 1 Data                           |
			:                                                               :
			+               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|               | NALU 2 Size                   | NALU 2 HDR    |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                         NALU 2 Data                           |
			:                                                               :
			|                               +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
			|                               :...OPTIONAL RTP padding        |
			+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+

			Figure 7.  An example of an RTP packet including an STAP-A
			containing two single-time aggregation units
		*/
		packet = packet[1:]
		for len(packet) >= 2 {
			size := int(packet[0])<<8 | int(packet[1])
			if size+2 > len(packet) {
				break
			}
			if err = self.handleH264Payload(timestamp, packet[2:size+2]); err != nil {
				return
			}
			packet = packet[size+2:]
		}
		return

	case naluType >= 6 && naluType <= 23: // other single NALU packet
	case naluType == 25: // STAB-B
	case naluType == 26: // MTAP-16
	case naluType == 27: // MTAP-24
	case naluType == 28: // FU-B

	default:
		err = fmt.Errorf("rtsp: unsupported H264 naluType=%d", naluType)
		return
	}

	return
}

func (s *Stream) handleRtpPacket(packet []byte) error {
	pkt := rtp.Packet{}
	err := pkt.Unmarshal(packet)
	if err != nil {
		return err
	}

	switch s.Sdp.Type {
	case av.H264:
		if err := s.handleH264Payload(pkt.Timestamp, pkt.Payload); err != nil {
			return err
		}

	default:
		return errors.New("RTP payload type not supportted")
	}
	return nil
}
