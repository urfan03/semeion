package datafeed

import "testing"

func TestPBReaderLengthOverflow(t *testing.T) {
	buf := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	p := &pbReader{buf: buf}
	if _, err := p.bytes(); err == nil {
		t.Fatal("a length prefix with bit 63 set must return a truncation error, not panic")
	}
}
