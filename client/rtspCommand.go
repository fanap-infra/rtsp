package client

import (
	"fmt"

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
