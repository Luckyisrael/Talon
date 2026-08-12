package scrcpy

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Control message types (from scrcpy protocol)
const (
	msgTypeInjectKeycode      = 0
	msgTypeInjectText         = 1
	msgTypeInjectTouchEvent   = 2
	msgTypeInjectScroll       = 3
	msgTypeBackOrScreenOn     = 4
	msgTypeExpandNotification = 5
	msgTypeExpandSettings     = 6
	msgTypeCollapsePanels     = 7
	msgTypeGetClipboard       = 8
	msgTypeSetClipboard       = 9
	msgTypeSetScreenPowerMode = 10
	msgTypeRotateDevice       = 11
)

// Device dimensions (Seeker device)
const (
	deviceWidth  = 1080
	deviceHeight = 2400
)

// Touch event actions
const (
	actionDown = 0
	actionUp   = 1
	actionMove = 2
)

// Key event actions
const (
	keyActionDown = 0
	keyActionUp   = 1
)

// ControlClient manages a persistent scrcpy control socket for input.
// This is fully decoupled from video — input continues working even if
// video capture is stalled, degraded, or disabled entirely.
type ControlClient struct {
	conn      net.Conn
	adbTarget string
	port      int
	scid      int
	mu        sync.Mutex
	closed    bool
}

// NewControlClient creates a new scrcpy control client.
func NewControlClient(adbTarget string) *ControlClient {
	return &ControlClient{
		adbTarget: adbTarget,
		port:      27183,
		scid:      1,
	}
}

// Start pushes scrcpy-server.jar to the device and opens a control-only socket.
// Uses scrcpy's binary protocol for low-latency input injection.
func (c *ControlClient) Start() error {
	// Find scrcpy-server.jar
	jarPath := findScrcpyServer()
	if jarPath == "" {
		return fmt.Errorf("scrcpy-server.jar not found")
	}

	// Push scrcpy-server.jar to device
	if err := c.pushServer(jarPath); err != nil {
		return fmt.Errorf("push scrcpy-server: %w", err)
	}

	// Start scrcpy-server in control-only mode (no video, no audio)
	if err := c.startServer(); err != nil {
		return fmt.Errorf("start scrcpy-server: %w", err)
	}

	// Set up ADB forward
	if err := c.adbForward(); err != nil {
		return fmt.Errorf("adb forward: %w", err)
	}

	// Connect to control socket
	if err := c.connect(); err != nil {
		return fmt.Errorf("connect to control socket: %w", err)
	}

	log.Printf("scrcpy control client connected (target=%s, port=%d)", c.adbTarget, c.port)
	return nil
}

func (c *ControlClient) pushServer(jarPath string) error {
	args := []string{"-s", c.adbTarget, "push", jarPath, "/data/local/tmp/scrcpy-server.jar"}
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("push failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (c *ControlClient) startServer() error {
	// Start scrcpy-server with control-only mode
	// video=false, audio=false, control=true
	args := []string{
		"-s", c.adbTarget, "shell",
		"CLASSPATH=/data/local/tmp/scrcpy-server.jar",
		"app_process", "/",
		"com.genymobile.scrcpy.Server", "2.1",
		"video=false",
		"audio=false",
		"control=true",
		"cleanup=false",
		fmt.Sprintf("scid=%d", c.scid),
		"log_level=info",
	}

	cmd := exec.Command("adb", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	// Wait briefly for server to start
	time.Sleep(500 * time.Millisecond)
	return nil
}

func (c *ControlClient) adbForward() error {
	// Remove any existing forward
	exec.Command("adb", "-s", c.adbTarget, "forward", "--remove", fmt.Sprintf("tcp:%d", c.port)).Run()

	// Set up new forward
	socketName := fmt.Sprintf("scrcpy_%08x", c.scid)
	args := []string{"-s", c.adbTarget, "forward", fmt.Sprintf("tcp:%d", c.port), fmt.Sprintf("localabstract:%s", socketName)}
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("forward failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (c *ControlClient) connect() error {
	// Connect to the forwarded port
	addr := fmt.Sprintf("localhost:%d", c.port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	// Read dummy byte (0x00) for forward connections
	dummy := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(dummy); err != nil {
		conn.Close()
		return fmt.Errorf("read dummy byte: %w", err)
	}
	conn.SetReadDeadline(time.Time{})

	// Read device metadata (device name)
	// Format: uint32 name_size, then name_size bytes (UTF-8)
	metaHeader := make([]byte, 4)
	if _, err := conn.Read(metaHeader); err != nil {
		conn.Close()
		return fmt.Errorf("read metadata header: %w", err)
	}
	nameSize := binary.LittleEndian.Uint32(metaHeader)
	if nameSize > 0 {
		nameBuf := make([]byte, nameSize)
		if _, err := conn.Read(nameBuf); err != nil {
			conn.Close()
			return fmt.Errorf("read device name: %w", err)
		}
		log.Printf("scrcpy connected to device: %s", string(nameBuf))
	}

	c.conn = conn
	return nil
}

// Tap sends a touch tap at (x, y) using scrcpy's INJECT_TOUCH_EVENT.
// This is a persistent connection — no new process per tap.
func (c *ControlClient) Tap(x, y int) error {
	return c.sendTouchEvent(actionDown, x, y, 0)
}

// Swipe sends a swipe gesture from (x1,y1) to (x2,y2) over durationMs.
func (c *ControlClient) Swipe(x1, y1, x2, y2, durationMs int) error {
	// For swipe, we send a sequence of MOVE events
	steps := durationMs / 16 // ~60fps
	if steps < 1 {
		steps = 1
	}

	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := int(float64(x1) + t*float64(x2-x1))
		y := int(float64(y1) + t*float64(y2-y1))

		action := actionMove
		if i == 0 {
			action = actionDown
		} else if i == steps {
			action = actionUp
		}

		if err := c.sendTouchEvent(action, x, y, 0); err != nil {
			return err
		}

		if i < steps {
			time.Sleep(16 * time.Millisecond) // ~60fps
		}
	}

	return nil
}

// Key sends a key event using scrcpy's INJECT_KEYCODE.
func (c *ControlClient) Key(keycode int) error {
	return c.sendKeyEvent(keyActionDown, keycode, 0)
}

func (c *ControlClient) sendTouchEvent(action, x, y, pointerID int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil {
		return fmt.Errorf("control client closed")
	}

	// INJECT_TOUCH_EVENT format (18 bytes):
	// [0]     type (0x02 = INJECT_TOUCH_EVENT)
	// [1]     action (0=DOWN, 1=UP, 2=MOVE)
	// [2-9]   pointer_id (int64, big-endian)
	// [10-11] position.x (uint16, big-endian)
	// [12-13] position.y (uint16, big-endian)
	// [14-15] width (uint16, big-endian)
	// [16-17] height (uint16, big-endian)
	// [18-21] pressure (uint32, big-endian, 0=MAX)

	buf := make([]byte, 22)
	buf[0] = msgTypeInjectTouchEvent
	buf[1] = byte(action)
	binary.BigEndian.PutUint64(buf[2:], uint64(pointerID))
	binary.BigEndian.PutUint16(buf[10:], uint16(x))
	binary.BigEndian.PutUint16(buf[12:], uint16(y))
	binary.BigEndian.PutUint16(buf[14:], uint16(deviceWidth))
	binary.BigEndian.PutUint16(buf[16:], uint16(deviceHeight))
	// Pressure: 0 = max pressure
	if action == actionUp {
		binary.BigEndian.PutUint32(buf[18:], 0)
	} else {
		binary.BigEndian.PutUint32(buf[18:], 0xFFFFFFFF)
	}

	_, err := c.conn.Write(buf)
	return err
}

func (c *ControlClient) sendKeyEvent(action, keycode, metastate int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil {
		return fmt.Errorf("control client closed")
	}

	// INJECT_KEYCODE format (10 bytes):
	// [0]     type (0x00 = INJECT_KEYCODE)
	// [1]     action (0=DOWN, 1=UP)
	// [2-5]   keycode (uint32, big-endian)
	// [6-9]   repeat (uint32, big-endian)
	// [10-13] metastate (uint32, big-endian)
	// [14-17] down_time (uint64, big-endian) -- not used

	buf := make([]byte, 18)
	buf[0] = msgTypeInjectKeycode
	buf[1] = byte(action)
	binary.BigEndian.PutUint32(buf[2:], uint32(keycode))
	binary.BigEndian.PutUint32(buf[6:], 0) // repeat
	binary.BigEndian.PutUint32(buf[10:], uint32(metastate))
	binary.BigEndian.PutUint64(buf[14:], 0) // down_time

	_, err := c.conn.Write(buf)
	return err
}

// Close shuts down the control client and cleans up.
func (c *ControlClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	// Remove ADB forward
	exec.Command("adb", "-s", c.adbTarget, "forward", "--remove", fmt.Sprintf("tcp:%d", c.port)).Run()

	// Kill scrcpy-server on device
	exec.Command("adb", "-s", c.adbTarget, "shell", "pkill", "-f", "scrcpy-server.jar").Run()

	log.Printf("scrcpy control client closed (target=%s)", c.adbTarget)
}

// findScrcpyServer looks for scrcpy-server.jar in common locations.
func findScrcpyServer() string {
	candidates := []string{
		"/usr/share/scrcpy/scrcpy-server.jar",
		"/usr/local/share/scrcpy/scrcpy-server.jar",
		filepath.Join(os.Getenv("HOME"), ".local/share/scrcpy/scrcpy-server.jar"),
		"/opt/scrcpy/scrcpy-server.jar",
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}

	// Try to find via which
	if out, err := exec.Command("which", "scrcpy").Output(); err == nil {
		scrcpyPath := strings.TrimSpace(string(out))
		if scrcpyPath != "" {
			// scrcpy binary is usually in bin/, server is in share/
			dir := filepath.Dir(scrcpyPath)
			candidate := filepath.Join(dir, "..", "share", "scrcpy", "scrcpy-server.jar")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}

	return ""
}
