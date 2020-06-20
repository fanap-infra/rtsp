package rtspcon

import (
	"net"
	"net/url"
	"time"
)

//NewTCPConn create new tcp connection
func NewTCPConn(
	urlString string,
	timeout time.Duration) (net.Conn, *url.URL, error) {
	url, err := url.Parse(urlString)
	if err != nil {
		return nil, nil, err
	}
	if _, _, err := net.SplitHostPort(url.Host); err != nil {
		url.Host = url.Host + ":554"
	}
	dailer := net.Dialer{Timeout: timeout}

	conn, err := dailer.Dial("tcp", url.Host)

	if err != nil {
		return nil, nil, err
	}
	return conn, url, nil
}

