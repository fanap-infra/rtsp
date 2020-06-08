package client

import (
	"fmt"
	"strings"

	"github.com/fanap-infra/rtsp/av"
	"github.com/fanap-infra/rtsp/sdp"
)

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

func (self *Client) allCodecDataReady() bool {
	for _, si := range self.setupIdx {
		stream := self.streams[si]
		if stream.CodecData == nil {
			return false
		}
	}
	return true
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
