package rtspcon

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/pion/sdp/v2"
)

const clHeader = "Content-Length:"
const clHeaderLen = len(clHeader)

const sessHeader = "Session:"
const sessHeaderLen = len(sessHeader)

const h264RtpMediaType = "96"

const (
	//StageIdle RTSP Idle stage
	StageIdle = 0
	//StageDescribeDone RTSP Describe stage
	StageDescribeDone = iota + 1
	//StageSetupDone RTSP Setup stage
	StageSetupDone
	//StagePlayDone RTSP Play stage
	StagePlayDone
)

//RtspConn manage connection to rtsp cameras.
type RtspConn struct {
	url           url.URL
	tcpReadWriter *bufio.ReadWriter

	sequenceNum uint
	authHeaders []string

	SDP sdp.SessionDescription

	Stage int

	sessionKey string
}

type request struct {
	header []string
	uri    string
	method string
}

//NewRtspConn Constructor for RtspConn.
func NewRtspConn(
	reader *bufio.ReadWriter,
	url url.URL) (*RtspConn, error) {

	rtspConn := RtspConn{}
	rtspConn.tcpReadWriter = reader
	rtspConn.url = url
	return &rtspConn, nil
}

//WriteRequest write RTSP request to connection.
func (conn *RtspConn) writeRequest(req request) error {
	conn.sequenceNum++

	fmt.Fprintf(conn.tcpReadWriter, "%s %s RTSP/1.0\r\n", req.method, req.uri)
	fmt.Fprintf(conn.tcpReadWriter, "CSeq: %d\r\n", conn.sequenceNum)

	if conn.authHeaders != nil {
		for _, s := range conn.authHeaders {
			io.WriteString(conn.tcpReadWriter, s)
			io.WriteString(conn.tcpReadWriter, "\r\n")
		}
	}
	for _, s := range req.header {
		io.WriteString(conn.tcpReadWriter, s)
		io.WriteString(conn.tcpReadWriter, "\r\n")
	}
	io.WriteString(conn.tcpReadWriter, "\r\n")
	conn.tcpReadWriter.Flush()
	return nil
}

func trimURLUser(url url.URL) string {
	newURL := url
	newURL.User = nil
	return newURL.String()
}

func (conn *RtspConn) readRtspResponse() ([]string, error) {
	header := make([]string, 0)
	for {
		line, _, err := conn.tcpReadWriter.ReadLine()
		if err != nil {
			return nil, err
		}
		if len(line) == 0 {
			break
		}
		header = append(header, string(line))
		conn.handleSDP(string(line), conn.tcpReadWriter)
		conn.handleSession(string(line))
	}
	return header, nil
}

func (conn *RtspConn) handleSDP(line string, reader *bufio.ReadWriter) error {
	if len(line) <= clHeaderLen || string(line[:clHeaderLen]) != clHeader {
		return nil
	}
	lenghStr := strings.Split(string(line), " ")[1]
	contetnLength, err := strconv.Atoi(lenghStr)
	if err != nil {
		return err
	}
	sdpByte := make([]byte, contetnLength)
	_, err = reader.Read(sdpByte)
	if err != nil {
		return err
	}
	sdp := sdp.SessionDescription{}
	err = sdp.Unmarshal(sdpByte)
	if err != nil {
		return err
	}
	conn.SDP = sdp
	return nil
}

func (conn *RtspConn) handleSession(line string) {
	if len(line) <= sessHeaderLen ||
		string(line[:sessHeaderLen]) != sessHeader {
		return
	}
	sessionKey := strings.Split(line, " ")[1]
	conn.sessionKey = sessionKey
	return
}
