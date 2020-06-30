package codec

import "github.com/fanap-infra/rtsp/av"

type MetadataData struct {
	uri string
	typ av.CodecType
}

func NewMetadataCodecData(uri string) av.CodecData {
	return MetadataData{
		uri: uri,
		typ: av.ONVIF_METADATA,
	}
}

func (m MetadataData) Type() av.CodecType {
	return m.typ
}

func (m MetadataData) URI() string {
	return m.uri
}
