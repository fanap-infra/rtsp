package client

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fanap-infra/rtsp/av"
)

var ErrCodecDataChange = fmt.Errorf("rtsp: codec data change, please call HandleCodecDataChange()")

var DebugRtp = false
var DebugRtsp = false
var SkipErrRtpBlock = false

const (
	stageDescribeDone = iota + 1
	stageSetupDone
	stageWaitCodecData
	stageCodecDataDone
)

type Client struct {
	Headers []string

	RtspTimeout          time.Duration
	RtpTimeout           time.Duration
	RtpKeepAliveTimeout  time.Duration
	rtpKeepaliveTimer    time.Time
	rtpKeepaliveEnterCnt int

	stage int

	setupIdx []int
	setupMap []int

	authHeaders func(method string) []string

	url         *url.URL
	conn        net.Conn
	brconn      *bufio.Reader
	cseq        uint
	streams     []*Stream
	streamsintf []av.CodecData
	session     string
	body        io.Reader
}

type Request struct {
	Header []string
	URI    string
	Method string
}

type Response struct {
	StatusCode    int
	Headers       textproto.MIMEHeader
	ContentLength int
	Body          []byte

	Block []byte
}

func DialTimeout(uri string, timeout time.Duration) (self *Client, err error) {
	var URL *url.URL
	if URL, err = url.Parse(uri); err != nil {
		return
	}

	if _, _, err := net.SplitHostPort(URL.Host); err != nil {
		URL.Host = URL.Host + ":554"
	}

	dailer := net.Dialer{Timeout: timeout}
	var conn net.Conn
	if conn, err = dailer.Dial("tcp", URL.Host); err != nil {
		return
	}

	self = &Client{
		conn:   conn,
		brconn: bufio.NewReaderSize(conn, 2048),
		url:    URL,
	}
	return
}

func Dial(uri string) (self *Client, err error) {
	if !strings.HasPrefix(uri, "rtsp://") {
		return nil, fmt.Errorf("RTSP doesn't support protocol: %s", uri)
	}

	return DialTimeout(uri, 0)
}

func trimURLUser(url *url.URL) string {
	newURL := url
	newURL.User = nil
	return newURL.String()
}

func (self *Client) Streams() (streams []av.CodecData, err error) {
	if err = self.prepare(stageCodecDataDone); err != nil {
		return
	}
	for _, si := range self.setupIdx {
		stream := self.streams[si]
		streams = append(streams, stream.CodecData)
	}
	return
}

func (self *Client) parseBlockHeader(h []byte) (length int, no int, valid bool) {
	length = int(h[2])<<8 + int(h[3])
	no = int(h[1])
	if no/2 >= len(self.streams) {
		return
	}

	if no%2 == 0 { // rtp
		if length < 8 {
			return
		}

		// V=2
		if h[4]&0xc0 != 0x80 {
			return
		}

		stream := self.streams[no/2]
		if int(h[5]&0x7f) != stream.Sdp.PayloadType {
			return
		}

		timestamp := binary.BigEndian.Uint32(h[8:12])
		if stream.firsttimestamp != 0 {
			timestamp -= stream.firsttimestamp
			if timestamp < stream.timestamp {
				return
			} else if timestamp-stream.timestamp > uint32(stream.timeScale()*60*60) {
				return
			}
		}
	}

	valid = true
	return
}

func (self *Client) parseHeaders(b []byte) (statusCode int, headers textproto.MIMEHeader, err error) {
	var line string
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(b)))
	if line, err = r.ReadLine(); err != nil {
		err = fmt.Errorf("rtsp: header invalid")
		return
	}

	if codes := strings.Split(line, " "); len(codes) >= 2 {
		if statusCode, err = strconv.Atoi(codes[1]); err != nil {
			err = fmt.Errorf("rtsp: header invalid: %s", err)
			return
		}
	}

	headers, _ = r.ReadMIMEHeader()
	return
}

func (self *Client) handleResp(res *Response) (err error) {
	if sess := res.Headers.Get("Session"); sess != "" && self.session == "" {
		if fields := strings.Split(sess, ";"); len(fields) > 0 {
			self.session = fields[0]
		}
	}
	if res.StatusCode == 401 {
		if err = self.handle401(res); err != nil {
			return
		}
	}
	return
}

func (self *Client) handle401(res *Response) (err error) {
	/*
		RTSP/1.0 401 Unauthorized
		CSeq: 2
		Date: Wed, May 04 2016 10:10:51 GMT
		WWW-Authenticate: Digest realm="LIVE555 Streaming Media", nonce="c633aaf8b83127633cbe98fac1d20d87"
	*/
	authval := res.Headers.Get("WWW-Authenticate")
	hdrval := strings.SplitN(authval, " ", 2)
	var realm, nonce string

	if len(hdrval) == 2 {
		for _, field := range strings.Split(hdrval[1], ",") {
			field = strings.Trim(field, ", ")
			if keyval := strings.Split(field, "="); len(keyval) == 2 {
				key := keyval[0]
				val := strings.Trim(keyval[1], `"`)
				switch key {
				case "realm":
					realm = val
				case "nonce":
					nonce = val
				}
			}
		}

		if realm != "" {
			var username string
			var password string

			if self.url.User == nil {
				err = fmt.Errorf("rtsp: no username")
				return
			}
			username = self.url.User.Username()
			password, _ = self.url.User.Password()

			self.authHeaders = func(method string) []string {
				var headers []string
				if nonce == "" {
					headers = []string{
						fmt.Sprintf(`Authorization: Basic %s`, base64.StdEncoding.EncodeToString([]byte(username+":"+password))),
					}
				} else {
					hs1 := md5hash(username + ":" + realm + ":" + password)
					hs2 := md5hash(method + ":" + trimURLUser(self.url))
					response := md5hash(hs1 + ":" + nonce + ":" + hs2)
					headers = []string{fmt.Sprintf(
						`Authorization: Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
						username, realm, nonce, trimURLUser(self.url), response)}
				}
				return headers
			}
		}
	}

	return
}
func (c *Client) findRTSP() (block []byte, header []byte, err error) {
	matchString := "RTSP"
	matchIndex := 0

	for {
		var b byte
		if b, err = c.brconn.ReadByte(); err != nil {
			return
		}
		if b == matchString[matchIndex] {
			matchIndex++
		} else {
			matchIndex = 0
		}
		if matchIndex == len(matchString) {
			//TODO: may be we should check for valid $ rtp packet here
			for {
				lfb, _ := c.brconn.ReadByte()
				header = append(header, lfb)
				if lfb != byte('\n') {
					continue
				}
				for i := 0; i < 3; i++ {
					nlfb, _ := c.brconn.ReadByte()
					header = append(header, nlfb)
					if nlfb == byte('\n') {
						return nil, header, nil
					}
				}

			}
		}

		if b == '$' {
			peek := []byte("$")
			readByte := make([]byte, 11)
			_, err = c.brconn.Read(readByte)
			if err != nil {
				return
			}
			peek = append(peek, readByte...)
			if blocklen, _, ok := c.parseBlockHeader(peek); ok {
				left := blocklen + 4 - len(peek)
				block = append(peek, make([]byte, left)...)
				if _, err = io.ReadFull(c.brconn, block[len(peek):]); err != nil {
					return
				}
				return
			}
		}
	}
}

func (self *Client) readResp(b []byte) (res Response, err error) {
	if res.StatusCode, res.Headers, err = self.parseHeaders(b); err != nil {
		return
	}
	res.ContentLength, _ = strconv.Atoi(res.Headers.Get("Content-Length"))
	if res.ContentLength > 0 {
		res.Body = make([]byte, res.ContentLength)
		if _, err = io.ReadFull(self.brconn, res.Body); err != nil {
			return
		}
	}
	if err = self.handleResp(&res); err != nil {
		return
	}
	return
}

func (self *Client) poll() (res Response, err error) {
	var block []byte
	var rtsp []byte
	var headers []byte

	for {
		if block, headers, err = self.findRTSP(); err != nil {
			return
		}
		if len(block) > 0 {
			res.Block = block
			return
		}
		if res, err = self.readResp(append(rtsp, headers...)); err != nil {
			return
		}

		return
	}

}

func (self *Client) ReadResponse() (res Response, err error) {
	for {
		if res, err = self.poll(); err != nil {
			return
		}
		if res.StatusCode > 0 {
			return
		}
	}
	return
}

func (self *Client) HandleCodecDataChange() (_newcli *Client, err error) {
	newcli := &Client{}
	*newcli = *self

	newcli.streams = []*Stream{}
	for _, stream := range self.streams {
		newstream := &Stream{}
		*newstream = *stream
		newstream.client = newcli

		if newstream.isCodecDataChange() {
			if err = newstream.makeCodecData(); err != nil {
				return
			}
			newstream.clearCodecDataChange()
		}
		newcli.streams = append(newcli.streams, newstream)
	}

	_newcli = newcli
	return
}

func (self *Client) handleBlock(block []byte) (pkt av.Packet, ok bool, err error) {
	_, blockno, _ := self.parseBlockHeader(block)
	if blockno%2 != 0 {
		if DebugRtp {
			fmt.Println("rtsp: rtcp block len", len(block)-4)
		}
		return
	}

	i := blockno / 2
	if i >= len(self.streams) {
		err = fmt.Errorf("rtsp: block no=%d invalid", blockno)
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

	if stream.gotpkt {
		/*
			TODO: sync AV by rtcp NTP timestamp
			TODO: handle timestamp overflow
			https://tools.ietf.org/html/rfc3550
			A receiver can then synchronize presentation of the audio and video packets by relating
			their RTP timestamps using the timestamp pairs in RTCP SR packets.
		*/
		if stream.firsttimestamp == 0 {
			stream.firsttimestamp = stream.timestamp
		}
		stream.timestamp -= stream.firsttimestamp

		ok = true
		pkt = stream.pkt
		pkt.Time = time.Duration(stream.timestamp) * time.Second / time.Duration(stream.timeScale())
		pkt.Idx = int8(self.setupMap[i])

		if pkt.Time < stream.lasttime || pkt.Time-stream.lasttime > time.Minute*30 {
			err = fmt.Errorf("rtp: time invalid stream#%d time=%v lasttime=%v", pkt.Idx, pkt.Time, stream.lasttime)
			return
		}
		stream.lasttime = pkt.Time

		if DebugRtp {
			fmt.Println("rtp: pktout", pkt.Idx, pkt.Time, len(pkt.Data))
		}

		stream.pkt = av.Packet{}
		stream.gotpkt = false
	}

	return
}

func (self *Client) readPacket() (pkt av.Packet, err error) {

	for {
		var res Response
		for {
			if res, err = self.poll(); err != nil {
				return
			}
			if len(res.Block) > 0 {
				break
			}
		}

		var ok bool
		if pkt, ok, err = self.handleBlock(res.Block); err != nil {
			return
		}
		if ok {
			return
		}
	}

	return
}

func (self *Client) ReadPacket() (pkt av.Packet, err error) {
	if err = self.prepare(stageCodecDataDone); err != nil {
		return
	}
	return self.readPacket()
}
