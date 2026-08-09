package adb

import (
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
)

type Client struct {
	target string
}

func New(target string) *Client {
	if target == "" {
		target = "localhost:5555"
	}
	return &Client{
		target: target,
	}
}

func (c *Client) adbArgs(args ...string) []string {
	adbArgs := []string{"-s", c.target}
	adbArgs = append(adbArgs, args...)
	return adbArgs
}

// Target returns the ADB target address (e.g., "localhost:15555").
func (c *Client) Target() string {
	return c.target
}

func (c *Client) Run(command string) (string, error) {
	args := c.adbArgs("shell", command)
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb shell failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) InstallAPK(apkPath string) (string, error) {
	args := c.adbArgs("install", "-r", apkPath)
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb install failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) Screenshot() ([]byte, error) {
	args := c.adbArgs("shell", "screencap", "-p", "/sdcard/screenshot.png")
	if err := exec.Command("adb", args...).Run(); err != nil {
		return nil, fmt.Errorf("screencap failed: %w", err)
	}

	args = c.adbArgs("pull", "/sdcard/screenshot.png", "/tmp/screenshot.png")
	if err := exec.Command("adb", args...).Run(); err != nil {
		return nil, fmt.Errorf("screenshot pull failed: %w", err)
	}

	out, err := exec.Command("cat", "/tmp/screenshot.png").Output()
	if err != nil {
		return nil, fmt.Errorf("screenshot read failed: %w", err)
	}

	return out, nil
}

func (c *Client) Logcat(filter string, lines int) (string, error) {
	args := c.adbArgs("logcat", "-d", "-t", fmt.Sprintf("%d", lines))
	if filter != "" {
		args = append(args, filter)
	}
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("logcat failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) IsConnected() bool {
	out, err := exec.Command("adb", "devices").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), c.target)
}

func (c *Client) Push(localPath, remotePath string) (string, error) {
	args := c.adbArgs("push", localPath, remotePath)
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb push failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) GetProp(prop string) (string, error) {
	return c.Run("getprop " + prop)
}

func (c *Client) Tap(x, y int) error {
	_, err := c.Run(fmt.Sprintf("input tap %d %d", x, y))
	return err
}

func (c *Client) Swipe(x1, y1, x2, y2, durationMs int) error {
	_, err := c.Run(fmt.Sprintf("input swipe %d %d %d %d %d", x1, y1, x2, y2, durationMs))
	return err
}

func (c *Client) Key(keycode string) error {
	_, err := c.Run("input keyevent " + keycode)
	return err
}

func (c *Client) ScreenshotStream() ([]byte, error) {
	args := c.adbArgs("exec-out", "screencap", "-p")
	out, err := exec.Command("adb", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("screencap stream failed: %w", err)
	}
	return out, nil
}

// ScreenshotRaw captures a raw RGBA framebuffer from the device.
// Returns raw pixel data (width * height * 4 bytes) and dimensions.
// This is significantly faster than ScreenshotStream because:
// 1. No on-device PNG compression
// 2. No PNG decode on host
// 3. Direct RGBA data for JPEG encoding
func (c *Client) ScreenshotRaw() ([]byte, int, int, error) {
	args := c.adbArgs("exec-out", "screencap")
	out, err := exec.Command("adb", args...).Output()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("raw screencap failed: %w", err)
	}

	// screencap without -p outputs a header followed by raw RGBA pixels.
	// Header format (little-endian):
	//   bytes 0-3:   width (uint32)
	//   bytes 4-7:   height (uint32)
	//   bytes 8-11:  format (uint32, 1 = RGBA_8888)
	//   bytes 12+:   pixel data (width * height * 4 bytes)
	if len(out) < 12 {
		return nil, 0, 0, fmt.Errorf("raw screencap output too short (%d bytes)", len(out))
	}

	width := int(binary.LittleEndian.Uint32(out[0:4]))
	height := int(binary.LittleEndian.Uint32(out[4:8]))
	// format := binary.LittleEndian.Uint32(out[8:12]) // 1 = RGBA_8888

	expectedSize := 12 + width*height*4
	if len(out) < expectedSize {
		return nil, 0, 0, fmt.Errorf("raw screencap: expected %d bytes, got %d", expectedSize, len(out))
	}

	return out[12:expectedSize], width, height, nil
}
