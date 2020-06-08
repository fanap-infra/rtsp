package client

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/fanap-infra/rtsp/av"
)

var errCodecDataChange = fmt.Errorf(
	"rtsp: codec data change, please callHandleCodecDataChange()")
var errRtpParseHeader = fmt.Errorf("can not parse rtp/tcp header")

//DebugRtp enable logs for RTP
var DebugRtp = false

//DebugRtsp enable logs for RTP
var DebugRtsp = false

//SkipErrRtpBlock ...
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

	conn, err := net.Dial("tcp4", URL.Host)
	if err != nil {
		return nil, err
	}

	self := &Client{
		conn:   conn,
		brconn: bufio.NewReaderSize(conn, 1024),
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

// func (self *Client) SendRtpKeepalive() (err error) {
// 	if self.RtpKeepAliveTimeout > 0 {
// 		if self.rtpKeepaliveTimer.IsZero() {
// 			self.rtpKeepaliveTimer = time.Now()
// 		} else if time.Now().Sub(self.rtpKeepaliveTimer) > self.RtpKeepAliveTimeout {
// 			self.rtpKeepaliveTimer = time.Now()
// 			if DebugRtsp {
// 				fmt.Println("rtp: keep alive")
// 			}
// 			req := Request{
// 				Method: "OPTIONS",
// 				Uri:    self.url.Host,
// 			}
// 			if err = self.WriteRequest(req); err != nil {
// 				return
// 			}
// 		}
// 	}
// 	return
// }

//WriteRequest write RTSP request to tcp connection.
func (c *Client) WriteRequest(req Request) error {
	c.cseq++

	buf := &bytes.Buffer{}

	fmt.Fprintf(buf, "%s %s RTSP/1.0\r\n", req.Method, req.Uri)
	fmt.Fprintf(buf, "CSeq: %d\r\n", c.cseq)

	if c.authHeaders != nil {
		headers := c.authHeaders(req.Method)
		for _, s := range headers {
			io.WriteString(buf, s)
			io.WriteString(buf, "\r\n")
		}
	}
	for _, s := range req.Header {
		io.WriteString(buf, s)
		io.WriteString(buf, "\r\n")
	}
	for _, s := range c.Headers {
		io.WriteString(buf, s)
		io.WriteString(buf, "\r\n")
	}
	io.WriteString(buf, "\r\n")

	bufout := buf.Bytes()

	if DebugRtsp {
		fmt.Print("> ", string(bufout))
	}
	_, err := c.conn.Write(bufout)
	if err != nil {
		return err
	}
	return nil
}

//parseRawTCP seperate rtsp from rtp packets.
//By searching for "RTSP" string
func (c *Client) parseRawTCP() (rtpPacket []byte, rtspHeader []byte, err error) {
	matchString := "RTSP"
	matchIndex := 0
	for {
		var b byte
		if b, err = c.brconn.ReadByte(); err != nil {
			return nil, nil, err
		}
		if b == matchString[matchIndex] {
			matchIndex++
		} else {
			matchIndex = 0
		}
		if matchIndex == len(matchString) {
			//TODO: maybe we should check for valid $ rtp packet here
			for {
				lfb, _ := c.brconn.ReadByte()
				rtspHeader = append(rtspHeader, lfb)
				if lfb != byte('\n') {
					continue
				}
				for i := 0; i < 3; i++ {
					nlfb, _ := c.brconn.ReadByte()
					rtspHeader = append(rtspHeader, nlfb)
					if nlfb == byte('\n') {
						return nil, rtspHeader, nil
					}
				}

			}
		}

		if b == '$' {
			peek := []byte("$")
			readByte := make([]byte, 11)
			_, err = c.brconn.Read(readByte)
			if err != nil {
				return nil, nil, err
			}
			peek = append(peek, readByte...)
			if blocklen, _, ok := c.parseRtpTCPHeader(peek); ok {
				left := int(blocklen) + 4 - len(peek)
				rtpPacket = append(peek, make([]byte, left)...)
				if _, err = io.ReadFull(c.brconn, rtpPacket[len(peek):]); err != nil {
					return nil, nil, err
				}
				return rtpPacket, nil, nil
			}
		}
	}
}

// pollTCPBuffer try to find RTP or RTSP packet in tcp connection bufio.
func (c *Client) pollTCPBuffer() (Response, error) {
	for {
		block, headers, err := c.parseRawTCP()
		if err != nil {
			return Response{}, err
		}
		if len(block) > 0 {
			response := Response{Block: block}
			return response, nil
		}
		response, err := c.readRtspResp(append([]byte("RTSP"), headers...))
		if err != nil {
			return Response{}, err
		}
		return response, nil
	}
}

//ReadResponse read Response from tcp connection could be rtsp or rtp packet.
func (c *Client) ReadResponse() (Response, error) {
	for {
		res, err := c.pollTCPBuffer()
		if err != nil {
			return Response{}, err
		}
		if res.StatusCode > 0 {
			return res, nil
		}
	}
}

//readPacket read from tcp Conn until find rtp packet
func (c *Client) readPacket() (av.Packet, error) {
	// if err = self.SendRtpKeepalive(); err != nil {
	// 	return
	// }
	for {
		var res Response
		for {
			res, err := c.pollTCPBuffer()
			if err != nil {
				return av.Packet{}, err
			}
			if len(res.Block) > 0 {
				//TODO: change len(res.Block) > 0 to isRTP maybe better approach
				break
			}
		}
		pkt, ok, err := c.handleRtpPacket(res.Block)
		if err != nil {
			return av.Packet{}, err
		}
		if ok {
			return pkt, nil
		}
	}
}

//ReadPacket read rtp packet from tcp connection
func (c *Client) ReadPacket() (pkt av.Packet, err error) {
	if err = c.prepare(stageCodecDataDone); err != nil {
		return
	}
	return c.readPacket()
}

// func (self *Client) HandleCodecDataChange() (_newcli *Client, err error) {
// 	newcli := &Client{}
// 	*newcli = *self

// 	newcli.streams = []*Stream{}
// 	for _, stream := range self.streams {
// 		newstream := &Stream{}
// 		*newstream = *stream
// 		newstream.client = newcli

// 		if newstream.isCodecDataChange() {
// 			if err = newstream.makeCodecData(); err != nil {
// 				return
// 			}
// 			newstream.clearCodecDataChange()
// 		}
// 		newcli.streams = append(newcli.streams, newstream)
// 	}

// 	_newcli = newcli
// 	return
// }
