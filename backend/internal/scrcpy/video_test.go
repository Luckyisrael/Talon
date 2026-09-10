package scrcpy

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// The video header parse is the fragile edge of DV3: it must consume
// exactly dummy + name(u32-len+bytes) + codec + size and leave the socket
// positioned at the NAL stream. A misparse feeds garbage to the browser's
// decoder — this test pins the byte layout against a synthetic server.
func TestVideoHeaderParse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := []byte{0x00} // the forward dummy byte
		name := []byte("redroid-pixel")
		var nameLen [4]byte
		binary.LittleEndian.PutUint32(nameLen[:], uint32(len(name)))
		buf = append(buf, nameLen[:]...)
		buf = append(buf, name...)
		var codec [4]byte
		binary.LittleEndian.PutUint32(codec[:], 0x34363268) // "h264" little-endian
		buf = append(buf, codec[:]...)
		var size [8]byte
		binary.LittleEndian.PutUint32(size[0:4], 720)
		binary.LittleEndian.PutUint32(size[4:8], 1600)
		buf = append(buf, size[:]...)
		// A trailing NAL start code + SPS header byte.
		buf = append(buf, 0, 0, 0, 1, 0x67)
		c.Write(buf)
		// Hold by reading: exits when the client closes its side.
		io.Copy(io.Discard, c)
	}()

	vc := &VideoClient{}
	if err := vc.connectTo(ln.Addr().String()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer vc.Close()

	if vc.Width != 720 || vc.Height != 1600 {
		t.Errorf("fault: parsed %dx%d, want 720x1600", vc.Width, vc.Height)
	}
	if vc.CodecID != "h264" {
		t.Errorf("fault: codec %q, want h264", vc.CodecID)
	}
	// Positioned at the NAL stream: the first bytes are the start code.
	one := make([]byte, 5)
	vc.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(vc.conn, one); err != nil || one[4] != 0x67 {
		t.Errorf("fault: stream not positioned at NALs (%v %v)", one, err)
	}
}

// Zero-size video is a hard error, not a guess: the browser cannot decode
// a dimensionless stream and the JPEG fallback must take over.
func TestVideoZeroSizeRejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := []byte{0x00}
		var nameLen [4]byte // zero-length name is legal
		buf = append(buf, nameLen[:]...)
		var codec [4]byte
		buf = append(buf, codec[:]...)
		var size [8]byte // 0x0
		buf = append(buf, size[:]...)
		c.Write(buf)
		io.Copy(io.Discard, c)
	}()

	vc := &VideoClient{}
	if err := vc.connectTo(ln.Addr().String()); err == nil {
		vc.Close()
		t.Fatal("fault: zero-size video accepted")
	}
}
