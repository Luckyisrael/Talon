// Package scrcpy's video side: a persistent H.264 tap off the device.
//
// scrcpy's server, started video=true control=false, pushes an H.264
// stream over its video socket: a dummy byte, device metadata, then the
// codec id and video size, then a raw NAL stream at the device encoder's
// rate. The device's hardware encoder does the work — the host relays
// bytes, nothing more. This replaces the 30fps screencap→JPEG pipeline
// (a CPU encode per frame) with the device's own encoder and a WebCodecs
// GPU decode in the browser.
package scrcpy

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"time"
)

// VideoClient is a read-only H.264 tap on one device.
type VideoClient struct {
	adbTarget string
	port      int
	scid      int
	listener  net.Listener
	conn      net.Conn
	Width     int
	Height    int
	CodecID   string
}

// NewVideoClient creates a video client for the target with its own
// scid/port — decoupled from the control socket, so a video failure never
// touches input.
func NewVideoClient() *VideoClient {
	return &VideoClient{port: 27184, scid: 2}
}

// Start pushes scrcpy-server (if not already there via control), starts it
// in video-only mode, and connects the video socket. It returns after the
// device header is parsed — Width/Height/CodecID are real from then on.
func (v *VideoClient) Start(adbTarget string) error {
	v.adbTarget = adbTarget
	jar := findScrcpyServer()
	if jar == "" {
		return fmt.Errorf("scrcpy-server.jar not found")
	}
	if err := v.pushServer(jar); err != nil {
		return fmt.Errorf("push scrcpy-server: %w", err)
	}
	if err := v.startServer(); err != nil {
		return fmt.Errorf("start scrcpy-server (video): %w", err)
	}
	if err := v.adbForward(); err != nil {
		return fmt.Errorf("adb forward (video): %w", err)
	}
	return v.connect()
}

func (v *VideoClient) pushServer(jarPath string) error {
	// The control socket pushes the same jar; a repeat push is a no-op on
	// the device (adb skips identical files) and keeps video self-contained.
	out, err := exec.Command("adb", "-s", v.adbTarget, "push", jarPath, "/data/local/tmp/scrcpy-server.jar").CombinedOutput()
	if err != nil {
		return fmt.Errorf("push failed: %s: %w", trimSpace(string(out)), err)
	}
	return nil
}

func (v *VideoClient) startServer() error {
	// video=true, control=false, audio=false: a pure H.264 tap. max_size
	// keeps 1080p frames under control for redroid; max_fps bounds the
	// device encoder's rate (the old pipeline was 30fps by ticker; the
	// device encoder is bounded the same way).
	args := []string{
		"-s", v.adbTarget, "shell",
		"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
		"app_process", "/",
		"com.genymobile.scrcpy.Server", "2.1",
		"video=true",
		"audio=false",
		"control=false",
		"cleanup=false",
		"max_size=1080",
		"max_fps=30",
		"video_bit_rate=2000000",
		fmt.Sprintf("scid=%d", v.scid),
		"log_level=info",
	}
	if err := exec.Command("adb", args...).Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

func (v *VideoClient) adbForward() error {
	exec.Command("adb", "-s", v.adbTarget, "forward", "--remove", fmt.Sprintf("tcp:%d", v.port)).Run()
	socketName := fmt.Sprintf("scrcpy_%08x", v.scid)
	out, err := exec.Command("adb", "-s", v.adbTarget, "forward", fmt.Sprintf("tcp:%d", v.port), fmt.Sprintf("localabstract:%s", socketName)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("forward failed: %s: %w", trimSpace(string(out)), err)
	}
	return nil
}

// connect reads the video socket's fixed header:
//
//	[dummy byte] [u32 name size][name] [u32 codec id][u32 width][u32 height]
//
// then leaves the conn positioned at the NAL stream. (scid disambiguates
// this socket from the control socket on the same adb target.)
func (v *VideoClient) connect() error {
	return v.connectTo(fmt.Sprintf("localhost:%d", v.port))
}

// connectTo dials the video socket and parses its header. Split from
// connect for the synthetic-socket test — the header layout is the
// contract, and the test pins it without adb or a device.
func (v *VideoClient) connectTo(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial video: %w", err)
	}

	// The forward dummy byte.
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		conn.Close()
		return fmt.Errorf("read dummy: %w", err)
	}

	// Device metadata (shared with the control socket's header).
	metaHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, metaHeader); err != nil {
		conn.Close()
		return fmt.Errorf("read metadata header: %w", err)
	}
	nameSize := binary.LittleEndian.Uint32(metaHeader)
	if nameSize > 0 && nameSize < 1<<16 {
		if _, err := io.ReadFull(conn, make([]byte, nameSize)); err != nil {
			conn.Close()
			return fmt.Errorf("read device name: %w", err)
		}
	}

	// scrcpy 2.x video socket: codec id (4 bytes) then size (4×2 bytes).
	codec := make([]byte, 4)
	if _, err := io.ReadFull(conn, codec); err != nil {
		conn.Close()
		return fmt.Errorf("read codec id: %w", err)
	}
	size := make([]byte, 8)
	if _, err := io.ReadFull(conn, size); err != nil {
		conn.Close()
		return fmt.Errorf("read video size: %w", err)
	}
	v.Width = int(binary.LittleEndian.Uint32(size[0:4]))
	v.Height = int(binary.LittleEndian.Uint32(size[4:8]))
	v.CodecID = string(codec)

	if v.Width == 0 || v.Height == 0 {
		conn.Close()
		return fmt.Errorf("device reported zero-size video (%dx%d)", v.Width, v.Height)
	}

	v.conn = conn
	log.Printf("scrcpy video connected (target=%s, %dx%d, codec=%q)", v.adbTarget, v.Width, v.Height, v.CodecID)
	return nil
}

// ReadChunk reads the next raw bytes of the NAL stream into buf. It
// returns on first error — the stream handler falls back to JPEG; a
// mid-session silence is the device's word, not ours to guess.
func (v *VideoClient) ReadChunk(buf []byte) (int, error) {
	return v.conn.Read(buf)
}

// Close drops the socket and the adb forward.
func (v *VideoClient) Close() {
	if v.conn != nil {
		v.conn.Close()
	}
	exec.Command("adb", "-s", v.adbTarget, "forward", "--remove", fmt.Sprintf("tcp:%d", v.port)).Run()
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
