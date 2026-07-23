package datafeed

import (
	"math"
	"testing"
)

func FuzzSnappyDecode(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add(snappyEncodeLiteral([]byte("hello world, a literal snappy block")))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = snappyDecode(data)
	})
}

func FuzzParseRemoteWrite(f *testing.F) {
	f.Add([]byte{})
	f.Add(snappyEncodeLiteral([]byte{0x0a, 0x00}))
	ts := int64(1767225600000)
	series := append(pbLenField(1, mkFuzzLabel("__name__", "m")), pbLenField(2, mkFuzzSample(1.5, ts))...)
	f.Add(snappyEncodeLiteral(pbLenField(1, series)))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseRemoteWrite(data)
	})
}

func mkFuzzLabel(name, value string) []byte {
	return append(pbString(1, name), pbString(2, value)...)
}
func mkFuzzSample(v float64, tms int64) []byte {
	return append(pbFixed64(1, math.Float64bits(v)), pbVarintField(2, uint64(tms))...)
}
