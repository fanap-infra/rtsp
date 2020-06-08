package client

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

func (c *Client) parseRtspHeaders(b []byte) (statusCode int, headers textproto.MIMEHeader, err error) {
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

func (c *Client) handleRtspSession(res *Response) error {
	sess := res.Headers.Get("Session")
	if sess != "" && c.session == "" {
		if fields := strings.Split(sess, ";"); len(fields) > 0 {
			c.session = fields[0]
		}
	}
	if res.StatusCode == 401 {
		if err := c.handle401(res); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) handle401(res *Response) error {
	/*
		RTSP/1.0 401 Unauthorized
		CSeq: 2
		Date: Wed, May 04 2016 10:10:51 GMT
		WWW-Authenticate: Digest realm="LIVE555 Streaming Media", nonce="c633aaf8b83127633cbe98fac1d20d87"
	*/
	authval := res.Headers.Get("WWW-Authenticate")
	hdrval := strings.SplitN(authval, " ", 2)
	var realm, nonce string

	if len(hdrval) != 2 {
		return nil
	}

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

	if realm == "" {
		return nil
	}

	var username string
	var password string

	if c.url.User == nil {
		err := fmt.Errorf("rtsp: no username")
		return err
	}

	username = c.url.User.Username()
	password, _ = c.url.User.Password()

	c.authHeaders = func(method string) []string {
		var headers []string
		if nonce == "" {
			headers = []string{
				fmt.Sprintf(`Authorization: Basic %s`, base64.StdEncoding.EncodeToString([]byte(username+":"+password))),
			}
		} else {
			hs1 := md5hash(username + ":" + realm + ":" + password)
			hs2 := md5hash(method + ":" + c.url.Host)
			response := md5hash(hs1 + ":" + nonce + ":" + hs2)
			headers = []string{fmt.Sprintf(
				`Authorization: Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
				username, realm, nonce, c.url.Host, response)}
		}
		return headers
	}

	return nil
}

func md5hash(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func (c *Client) readRtspResp(b []byte) (Response, error) {
	var res Response
	var err error
	res.StatusCode, res.Headers, err = c.parseRtspHeaders(b)
	if err != nil {
		return Response{}, err
	}
	res.ContentLength, _ = strconv.Atoi(res.Headers.Get("Content-Length"))
	if res.ContentLength > 0 {
		res.Body = make([]byte, res.ContentLength)
		if _, err = io.ReadFull(c.brconn, res.Body); err != nil {
			return Response{}, err
		}
	}
	if err = c.handleRtspSession(&res); err != nil {
		return Response{}, err
	}
	return res, nil
}
