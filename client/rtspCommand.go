package client

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/fanap-infra/rtsp/sdp"
)

func (self *Client) WriteRequest(req Request) (err error) {
	// self.conn.Timeout = self.RtspTimeout
	self.cseq++

	buf := &bytes.Buffer{}

	fmt.Fprintf(buf, "%s %s RTSP/1.0\r\n", req.Method, req.URI)
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
	if _, err = self.conn.Write(bufout); err != nil {
		return
	}

	return
}

func (self *Client) Play() (err error) {
	req := Request{
		Method: "PLAY",
		URI:    trimURLUser(self.url),
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
		URI:    trimURLUser(self.url),
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

func (self *Client) Describe() (streams []sdp.Media, err error) {
	var res Response

	for i := 0; i < 2; i++ {
		req := Request{
			Method: "DESCRIBE",
			URI:    trimURLUser(self.url),
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
		URI:    trimURLUser(self.url),
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
			uri = trimURLUser(self.url) + "/" + control
		}
		req := Request{Method: "SETUP", URI: uri}
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
