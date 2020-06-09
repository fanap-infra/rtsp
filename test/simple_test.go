package simple_test

import (
	"os"
	"testing"

	"github.com/fanap-infra/rtsp/client"
	"github.com/stretchr/testify/suite"
)

type TestSuite struct {
	suite.Suite
	client *client.Client
}

func (suite *TestSuite) SetupTest() {
	client, err := client.Dial("rtsp://127.0.0.1:1234/test")
	if err != nil {
		suite.Fail("Cliant create failed", err.Error())
	}
	suite.client = client
}

func (suite *TestSuite) TestReadPacket() {
	pkt, err := suite.client.ReadPacket()
	if err != nil {
		suite.Fail("ReadPacket failed", err.Error())
	}
	file, err := os.Open("test.raw")
	if err != nil {
		suite.Fail("Open tes.raw failed", err.Error())
	}
	if len(pkt.Data) < 10000 {
		suite.Fail("Small packet")
	}
	expected := make([]byte, len(pkt.Data))
	n, err := file.Read(expected)
	if err != nil {
		suite.Fail("Read tes.raw failed", err.Error())
	}
	if n != len(pkt.Data) {
		suite.Fail("Read tes.raw failed. lengh doesn't match")
	}
	suite.Equal(pkt.Data, expected)
}

func TestExampleTestSuite(t *testing.T) {
	suite.Run(t, new(TestSuite))
}
