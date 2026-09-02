package tray

import (
	"bytes"
	"encoding/binary"
)

// buildICO renders a 16x16 filled circle as a Windows .ico in memory.
//
// The icon is generated rather than shipped as a binary asset so there is nothing to
// keep in sync, and so the three states differ only by the colour passed in here.
func buildICO(r, g, b uint8) []byte {
	const (
		size       = 16
		xorBytes   = size * size * 4 // 32bpp BGRA
		maskStride = 4               // 16 bits padded to a 4-byte boundary
		maskBytes  = size * maskStride
		dibHeader  = 40
	)

	buf := &bytes.Buffer{}

	// ICONDIR
	binary.Write(buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(buf, binary.LittleEndian, uint16(1)) // image count

	// ICONDIRENTRY
	buf.WriteByte(size)                                                          // width
	buf.WriteByte(size)                                                          // height
	buf.WriteByte(0)                                                             // palette size (0 = truecolour)
	buf.WriteByte(0)                                                             // reserved
	binary.Write(buf, binary.LittleEndian, uint16(1))                            // colour planes
	binary.Write(buf, binary.LittleEndian, uint16(32))                           // bits per pixel
	binary.Write(buf, binary.LittleEndian, uint32(dibHeader+xorBytes+maskBytes)) // bytes in resource
	binary.Write(buf, binary.LittleEndian, uint32(22))                           // offset to image

	// BITMAPINFOHEADER. Height is doubled because an icon DIB stores the colour image
	// and the AND mask stacked in one bitmap.
	binary.Write(buf, binary.LittleEndian, uint32(dibHeader))
	binary.Write(buf, binary.LittleEndian, int32(size))
	binary.Write(buf, binary.LittleEndian, int32(size*2))
	binary.Write(buf, binary.LittleEndian, uint16(1))
	binary.Write(buf, binary.LittleEndian, uint16(32))
	binary.Write(buf, binary.LittleEndian, uint32(0)) // BI_RGB
	binary.Write(buf, binary.LittleEndian, uint32(xorBytes+maskBytes))
	binary.Write(buf, binary.LittleEndian, int32(0))
	binary.Write(buf, binary.LittleEndian, int32(0))
	binary.Write(buf, binary.LittleEndian, uint32(0))
	binary.Write(buf, binary.LittleEndian, uint32(0))

	// Colour data, bottom-up as DIBs require, BGRA with a straight alpha edge.
	const centre = (size - 1) / 2.0
	const radius = 7.0
	for row := size - 1; row >= 0; row-- {
		for col := 0; col < size; col++ {
			dx := float64(col) - centre
			dy := float64(row) - centre
			inside := dx*dx+dy*dy <= radius*radius
			if inside {
				buf.WriteByte(b)
				buf.WriteByte(g)
				buf.WriteByte(r)
				buf.WriteByte(0xFF)
			} else {
				buf.Write([]byte{0, 0, 0, 0}) // transparent
			}
		}
	}

	// AND mask: unused for 32bpp icons (alpha carries transparency) but the bitmap must
	// still be there or Windows rejects the icon.
	buf.Write(make([]byte, maskBytes))

	return buf.Bytes()
}

var (
	// Grey: running but not connected to the server.
	iconOffline = buildICO(0x94, 0xA3, 0xB8)
	// Blue: connected and available to be viewed.
	iconOnline = buildICO(0x38, 0xBD, 0xF8)
	// Red: someone is watching this screen right now.
	iconInSession = buildICO(0xE1, 0x1D, 0x48)
)
