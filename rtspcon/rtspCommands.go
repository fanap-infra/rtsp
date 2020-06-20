package rtspcon

import (
	"errors"
	"fmt"
	"strings"
)

//Play RTSP command.
func (conn *RtspConn) Play() error {
	if err := conn.PrepareStage(StageSetupDone); err != nil {
		return err
	}
	req := request{
		method: "PLAY",
		uri:    trimURLUser(conn.url),
	}
	req.header = append(req.header, "Session: "+conn.sessionKey)
	if err := conn.writeRequest(req); err != nil {
		return err
	}
	headers, err := conn.readRtspResponse()
	if err != nil {
		return err
	}
	if string(headers[0]) != "RTSP/1.0 200 OK" {
		return errors.New("Describe failed")
	}
	conn.Stage = StagePlayDone
	return nil
}

//Describe RTSP Describe command
func (conn *RtspConn) Describe() error {
	req := request{
		method: "DESCRIBE",
		uri:    trimURLUser(conn.url),
		header: []string{"Accept: application/sdp"},
	}
	if err := conn.writeRequest(req); err != nil {
		return err
	}
	headers, err := conn.readRtspResponse()
	if err != nil {
		return err
	}
	if string(headers[0]) != "RTSP/1.0 200 OK" {
		return errors.New("Describe failed")
	}
	if len(conn.SDP.MediaDescriptions) == 0 {
		return errors.New("Describe failed, Cannot read SDP")
	}
	conn.Stage = StageDescribeDone
	return nil
}

//Setup RTSP command
func (conn *RtspConn) Setup() error {
	if err := conn.PrepareStage(StageDescribeDone); err != nil {
		return err
	}
	for index, mediaDesc := range conn.SDP.MediaDescriptions[:1] {
		uri := ""
		control, _ := mediaDesc.Attribute("control")
		if strings.HasPrefix(control, "rtsp://") {
			uri = control
		} else {
			uri = trimURLUser(conn.url) + "/" + control
		}
		req := request{method: "SETUP", uri: uri}
		req.header = append(req.header,
			fmt.Sprintf("Transport: RTP/AVP/TCP;unicast;interleaved=%d-%d",
				index*2, index*2+1))
		if conn.sessionKey != "" {
			req.header = append(req.header, "Session: "+conn.sessionKey)
		}
		if err := conn.writeRequest(req); err != nil {
			return err
		}
		if _, err := conn.readRtspResponse(); err != nil {
			return err
		}
	}
	conn.Stage = StageSetupDone
	return nil
}

//PrepareStage given RTSP stage
func (conn *RtspConn) PrepareStage(stage int) error {
	for conn.Stage < stage {
		switch conn.Stage {
		case StageIdle:
			if err := conn.Describe(); err != nil {
				return err
			}
		case StageDescribeDone:
			if err := conn.Setup(); err != nil {
				return err
			}
		case StageSetupDone:
			if err := conn.Play(); err != nil {
				return err
			}
		}
	}
	return nil
}
