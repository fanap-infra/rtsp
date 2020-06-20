package rtspcon

import (
	"bufio"
	"log"
	"os"
	"runtime/trace"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type rtspConnSuite struct {
	suite.Suite
	conn    *RtspConn
	rtpConn *RtpCon
	url     string
}

func (suite *rtspConnSuite) SetupTest() {
	tcpConn, url, err := NewTCPConn(suite.url, 1*time.Minute)
	if err != nil {
		suite.Fail("NewTCPConn failed", err.Error())
	}
	reader := bufio.NewReaderSize(tcpConn, 4096)
	writer := bufio.NewWriter(tcpConn)
	readWriter := bufio.NewReadWriter(reader, writer)
	client, err := NewRtspConn(readWriter, *url)
	if err != nil {
		suite.Fail("NewRtspConn failed", err.Error())
	}
	rtpConn := RtpCon{reader: reader, sleepDuration: 30 * time.Millisecond}
	suite.rtpConn = &rtpConn
	suite.conn = client
}

// func (suite *rtspConnSuite) TestDescribe() {
// 	suite.conn.Describe()
// 	suite.Equal(suite.conn.Stage, StageDescribeDone)
// }

// func (suite *rtspConnSuite) TestSetup() {
// 	suite.conn.Setup()
// 	suite.Equal(suite.conn.Stage, StageSetupDone)
// }

// func (suite *rtspConnSuite) TestPlay() {
// 	suite.conn.Play()
// 	suite.Equal(suite.conn.Stage, StagePlayDone)
// }

func (suite *rtspConnSuite) TestRtpReadPacket() {
	// var cpuprofile = flag.String("cpuprofile", "./cpu.prof", "write cpu profile to file")

	suite.conn.Play()

	ch := suite.rtpConn.ReadTCPPacket()
	c := 0
	for pkt := range ch {
		if c == 400 {
			break
		}
		c++
		suite.Equal(pkt.Header.PayloadType, uint8(35))
	}
}

func Test(t *testing.T) {
	t.Parallel()
	f, err := os.Create("./app.trace")
	if err != nil {
		log.Fatal(err)
	}
	// pprof.
	// pprof.StartCPUProfile(f)
	// defer pprof.StopCPUProfile()

	trace.Start(f)
	defer trace.Stop()

	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.83"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.162"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.161"})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.129", wg: wg})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.14.23", wg: wg})
	// go suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.152", wg: wg})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.83"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.162"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.161"})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.129", wg: wg})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.14.23", wg: wg})
	// go suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.152", wg: wg})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.83"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.162"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.161"})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.129", wg: wg})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.14.23", wg: wg})
	// go suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.152", wg: wg})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.83"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.162"})
	suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.11.161"})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.129", wg: wg})
	// suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.14.23", wg: wg})
	// go suite.Run(t, &rtspConnSuite{url: "rtsp://192.168.15.152", wg: wg})
}
