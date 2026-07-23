package datafeed

import (
	"encoding/binary"
	"fmt"
)

func snappyDecode(src []byte) ([]byte, error) {
	dLen64, n := binary.Uvarint(src)
	if n <= 0 {
		return nil, fmt.Errorf("snappy: bad length prefix")
	}
	if dLen64 > 1<<32 {
		return nil, fmt.Errorf("snappy: implausible decoded length %d", dLen64)
	}
	dLen := int(dLen64)
	dst := make([]byte, 0, dLen)
	i := n
	for i < len(src) {
		tag := src[i]
		switch tag & 0x03 {
		case 0x00:
			length := int(tag >> 2)
			i++
			if length >= 60 {
				extra := length - 59
				if i+extra > len(src) {
					return nil, fmt.Errorf("snappy: truncated literal length")
				}
				length = 0
				for j := 0; j < extra; j++ {
					length |= int(src[i+j]) << (8 * j)
				}
				i += extra
			}
			length++
			if i+length > len(src) {
				return nil, fmt.Errorf("snappy: truncated literal")
			}
			dst = append(dst, src[i:i+length]...)
			i += length
		case 0x01:
			if i+1 >= len(src) {
				return nil, fmt.Errorf("snappy: truncated copy1")
			}
			length := int((tag>>2)&0x07) + 4
			offset := int(tag>>5)<<8 | int(src[i+1])
			i += 2
			var err error
			if dst, err = snappyCopy(dst, offset, length); err != nil {
				return nil, err
			}
		case 0x02:
			if i+2 >= len(src) {
				return nil, fmt.Errorf("snappy: truncated copy2")
			}
			length := int(tag>>2) + 1
			offset := int(binary.LittleEndian.Uint16(src[i+1:]))
			i += 3
			var err error
			if dst, err = snappyCopy(dst, offset, length); err != nil {
				return nil, err
			}
		case 0x03:
			if i+4 >= len(src) {
				return nil, fmt.Errorf("snappy: truncated copy4")
			}
			length := int(tag>>2) + 1
			offset := int(binary.LittleEndian.Uint32(src[i+1:]))
			i += 5
			var err error
			if dst, err = snappyCopy(dst, offset, length); err != nil {
				return nil, err
			}
		}
	}
	if len(dst) != dLen {
		return nil, fmt.Errorf("snappy: decoded %d bytes, header claimed %d", len(dst), dLen)
	}
	return dst, nil
}

func snappyCopy(dst []byte, offset, length int) ([]byte, error) {
	if offset <= 0 || offset > len(dst) {
		return nil, fmt.Errorf("snappy: bad copy offset %d (have %d)", offset, len(dst))
	}
	start := len(dst) - offset
	for k := 0; k < length; k++ {
		dst = append(dst, dst[start+k])
	}
	return dst, nil
}
