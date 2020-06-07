package client

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/sdp"
)

var errCodecDataChange = fmt.Errorf("rtsp: codec data change, please call HandleCodecDataChange()")
var errRtpParseHeader = fmt.Errorf("can not parse rtp/tcp header")

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

	RtpKeepAliveTimeout time.Duration
	rtpKeepaliveTimer   time.Time

	stage int

	setupIdx []int
	setupMap []int

	authHeaders func(method string) []string

	url     *url.URL
	conn    net.Conn
	brconn  *bufio.Reader
	cseq    uint
	streams []*Stream
	session string
}

type Request struct {
	Header []string
	Uri    string
	Method string
}

type Response struct {
	StatusCode    int
	Headers       textproto.MIMEHeader
	ContentLength int
	Body          []byte

	Block []byte
}

//TODO: I removed timeout beacause may conflict with keep-alive. Add it when needed.
//We should handle 1-rtp 2-rtsp 3-dialer timeouts also considering keep-alive

//DialTimeout create RTSP client over TCP with timeout.
//with defalut reder size=2048, port=554
func DialTimeout(uri string, timeout time.Duration) (*Client, error) {
	URL, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	if URL.Port() == "" {
		URL.Host = URL.Host + ":554"
	}

	dailer := net.Dialer{Timeout: timeout}
	conn, err := dailer.Dial("tcp", URL.Host)
	// conn.SetDeadline(time.Now().Add(timeout))
	if err != nil {
		return nil, err
	}

	self := &Client{
		conn:   conn,
		brconn: bufio.NewReaderSize(conn, 2048),
		url:    URL,
	}
	return self, nil
}

//Dial setup RTSP client with default timeout=0.
//It calls DialTimeout(uri, 0)
func Dial(uri string) (*Client, error) {
	if !strings.HasPrefix(uri, "rtsp://") {
		return nil, fmt.Errorf("RTSP doesn't support protocol: %s", uri)
	}
	return DialTimeout(uri, 0)
}

func (self *Client) allCodecDataReady() bool {
	for _, si := range self.setupIdx {
		stream := self.streams[si]
		if stream.CodecData == nil {
			return false
		}
	}
	return true
}

func (self *Client) probe() (err error) {
	for {
		if self.allCodecDataReady() {
			break
		}
		if _, err = self.readPacket(); err != nil {
			return
		}
	}
	self.stage = stageCodecDataDone
	return
}

func (self *Client) prepare(stage int) (err error) {
	for self.stage < stage {
		switch self.stage {
		case 0:
			if _, err = self.Describe(); err != nil {
				return
			}

		case stageDescribeDone:
			if err = self.SetupAll(); err != nil {
				return
			}

		case stageSetupDone:
			if err = self.Play(); err != nil {
				return
			}

		case stageWaitCodecData:
			if err = self.probe(); err != nil {
				return
			}
		}
	}
	return
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

func (self *Client) SendRtpKeepalive() (err error) {
	if self.RtpKeepAliveTimeout > 0 {
		if self.rtpKeepaliveTimer.IsZero() {
			self.rtpKeepaliveTimer = time.Now()
		} else if time.Now().Sub(self.rtpKeepaliveTimer) > self.RtpKeepAliveTimeout {
			self.rtpKeepaliveTimer = time.Now()
			if DebugRtsp {
				fmt.Println("rtp: keep alive")
			}
			req := Request{
				Method: "OPTIONS",
				Uri:    self.url.Host,
			}
			if err = self.WriteRequest(req); err != nil {
				return
			}
		}
	}
	return
}

func (self *Client) WriteRequest(req Request) (err error) {
	// self.conn.SetDeadline()
	// self.conn.SetDeadline()
	// self.conn.Timeout = self.RtspTimeout
	self.cseq++

	buf := &bytes.Buffer{}

	fmt.Fprintf(buf, "%s %s RTSP/1.0\r\n", req.Method, req.Uri)
	fmt.Fprintf(buf, "CSeq: %d\r\n", self.cseq)

	if self.authHeaders != nil {
		headers := self.authHeaders(req.Method)
		for _, s := range headers {
			io.WriteString(buf, s)
			io.WriteString(buf, "\r\n")
		}
	}
	for _, s := range req.Header {
		io.WriteString(buf, s)
		io.WriteString(buf, "\r\n")
	}
	for _, s := range self.Headers {
		io.WriteString(buf, s)
		io.WriteString(buf, "\r\n")
	}
	io.WriteString(buf, "\r\n")

	bufout := buf.Bytes()

	if DebugRtsp {
		fmt.Print("> ", string(bufout))
	}
	_, err = self.conn.Write(bufout)
	if err != nil {
		return
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
					hs2 := md5hash(method + ":" + self.url.Host)
					response := md5hash(hs1 + ":" + nonce + ":" + hs2)
					headers = []string{fmt.Sprintf(
						`Authorization: Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
						username, realm, nonce, self.url.Host, response)}
				}
				return headers
			}
		}
	}

	return
}
func (self *Client) findRTSP() (block []byte, data []byte, err error) {
	const (
		R = iota + 1
		T
		S
		Header
		Dollar
	)
	var _peek [8]byte
	peek := _peek[0:0]
	stat := 0

	for i := 0; ; i++ {
		var b byte
		if b, err = self.brconn.ReadByte(); err != nil {
			return
		}
		switch b {
		case 'R':
			if stat == 0 {
				stat = R
			}
		case 'T':
			if stat == R {
				stat = T
			}
		case 'S':
			if stat == T {
				stat = S
			}
		case 'P':
			if stat == S {
				stat = Header
			}
		case '$':
			if stat != Dollar {
				stat = Dollar
				peek = _peek[0:0]
			}
		default:
			if stat != Dollar {
				stat = 0
				peek = _peek[0:0]
			}
		}

		if false && DebugRtp {
			fmt.Println("rtsp: findRTSP", i, b)
		}

		if stat != 0 {
			peek = append(peek, b)
		}
		if stat == Header {
			data = peek
			return
		}

		if stat == Dollar && len(peek) >= 12 {
			if DebugRtp {
				fmt.Println("rtsp: dollar at", i, len(peek))
			}
			if blocklen, _, ok := self.parseBlockHeader(peek); ok {
				left := blocklen + 4 - len(peek)
				block = append(peek, make([]byte, left)...)
				if _, err = io.ReadFull(self.brconn, block[len(peek):]); err != nil {
					return
				}
				return
			}
			stat = 0
			peek = _peek[0:0]
		}
	}

	return
}

func (self *Client) readLFLF() (block []byte, data []byte, err error) {
	const (
		LF = iota + 1
		LFLF
	)
	peek := []byte{}
	stat := 0
	dollarpos := -1
	lpos := 0
	pos := 0

	for {
		var b byte
		if b, err = self.brconn.ReadByte(); err != nil {
			return
		}
		switch b {
		case '\n':
			if stat == 0 {
				stat = LF
				lpos = pos
			} else if stat == LF {
				if pos-lpos <= 2 {
					stat = LFLF
				} else {
					lpos = pos
				}
			}
		case '$':
			dollarpos = pos
		}
		peek = append(peek, b)

		if stat == LFLF {
			data = peek
			return
		} else if dollarpos != -1 && dollarpos-pos >= 12 {
			hdrlen := dollarpos - pos
			start := len(peek) - hdrlen
			if blocklen, _, ok := self.parseBlockHeader(peek[start:]); ok {
				block = append(peek[start:], make([]byte, blocklen+4-hdrlen)...)
				if _, err = io.ReadFull(self.brconn, block[hdrlen:]); err != nil {
					return
				}
				return
			}
			dollarpos = -1
		}

		pos++
	}

	return
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

	// self.conn.Timeout = self.RtspTimeout
	for {
		if block, rtsp, err = self.findRTSP(); err != nil {
			return
		}
		if len(block) > 0 {
			res.Block = block
			return
		}
		if block, headers, err = self.readLFLF(); err != nil {
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

func (self *Client) SetupAll() (err error) {
	idx := []int{}
	for i := range self.streams {
		idx = append(idx, i)
	}
	return self.Setup(idx)
}

func (self *Client) Setup(idx []int) (err error) {
	if err = self.prepare(stageDescribeDone); err != nil {
		return
	}

	self.setupMap = make([]int, len(self.streams))
	for i := range self.setupMap {
		self.setupMap[i] = -1
	}
	self.setupIdx = idx

	for i, si := range idx {
		self.setupMap[si] = i

		uri := ""
		control := self.streams[si].Sdp.Control
		if strings.HasPrefix(control, "rtsp://") {
			uri = control
		} else {
			uri = self.url.Host + "/" + control
		}
		req := Request{Method: "SETUP", Uri: uri}
		req.Header = append(req.Header, fmt.Sprintf("Transport: RTP/AVP/TCP;unicast;interleaved=%d-%d", si*2, si*2+1))
		if self.session != "" {
			req.Header = append(req.Header, "Session: "+self.session)
		}
		if err = self.WriteRequest(req); err != nil {
			return
		}
		if _, err = self.ReadResponse(); err != nil {
			return
		}
	}

	if self.stage == stageDescribeDone {
		self.stage = stageSetupDone
	}
	return
}

func md5hash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func (self *Client) Describe() (streams []sdp.Media, err error) {
	var res Response

	for i := 0; i < 2; i++ {
		req := Request{
			Method: "DESCRIBE",
			Uri:    self.url.Host,
			Header: []string{"Accept: application/sdp"},
		}
		if err = self.WriteRequest(req); err != nil {
			return
		}
		if res, err = self.ReadResponse(); err != nil {
			return
		}
		if res.StatusCode == 200 {
			break
		}
	}
	if res.ContentLength == 0 || res.ContentLength != len(res.Body) {
		err = fmt.Errorf("rtsp: Describe failed, StatusCode=%d", res.StatusCode)
		return
	}

	body := string(res.Body)

	if DebugRtsp {
		fmt.Println("<", body)
	}

	_, medias := sdp.Parse(body)

	self.streams = []*Stream{}
	for _, media := range medias {
		stream := &Stream{Sdp: media, client: self}
		stream.makeCodecData()
		self.streams = append(self.streams, stream)
		streams = append(streams, media)
	}

	if self.stage == 0 {
		self.stage = stageDescribeDone
	}
	return
}

func (self *Client) Options() (err error) {
	req := Request{
		Method: "OPTIONS",
		Uri:    self.url.Host,
	}
	if self.session != "" {
		req.Header = append(req.Header, "Session: "+self.session)
	}
	if err = self.WriteRequest(req); err != nil {
		return
	}
	if _, err = self.ReadResponse(); err != nil {
		return
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

func (self *Client) Play() (err error) {
	req := Request{
		Method: "PLAY",
		Uri:    self.url.Host,
	}
	req.Header = append(req.Header, "Session: "+self.session)
	if err = self.WriteRequest(req); err != nil {
		return
	}

	if self.allCodecDataReady() {
		self.stage = stageCodecDataDone
	} else {
		self.stage = stageWaitCodecData
	}
	return
}

func (self *Client) Teardown() (err error) {
	req := Request{
		Method: "TEARDOWN",
		Uri:    self.url.Host,
	}
	req.Header = append(req.Header, "Session: "+self.session)
	if err = self.WriteRequest(req); err != nil {
		return
	}
	return
}

func (self *Client) Close() (err error) {
	return self.conn.Close()
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
	if err = self.SendRtpKeepalive(); err != nil {
		return
	}

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
}

func (self *Client) ReadPacket() (pkt av.Packet, err error) {
	if err = self.prepare(stageCodecDataDone); err != nil {
		return
	}
	return self.readPacket()
}
