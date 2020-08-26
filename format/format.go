package format

import (
	"github.com/fanap-infra/rtsp/av/avutil"
	"github.com/fanap-infra/rtsp/format/mp4"
)

func RegisterAll() {
	avutil.DefaultHandlers.Add(mp4.Handler)
}
