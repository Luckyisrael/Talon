package adb

import (
	"fmt"
	"os/exec"
	"strings"
)

type Client struct {
	wslDistro string
	target    string
}

func New() *Client {
	return &Client{
		wslDistro: "SeekerLinux",
		target:    "localhost:5555",
	}
}

func (c *Client) wslArgs(args ...string) []string {
	allArgs := []string{"-d", c.wslDistro, "-e"}
	allArgs = append(allArgs, args...)
	return allArgs
}

func (c *Client) adbArgs(args ...string) []string {
	adbArgs := []string{"adb", "-s", c.target}
	adbArgs = append(adbArgs, args...)
	return c.wslArgs(adbArgs...)
}

func (c *Client) Run(command string) (string, error) {
	args := c.adbArgs("shell", command)
	out, err := exec.Command("wsl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb shell failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) InstallAPK(apkPath string) (string, error) {
	args := c.adbArgs("install", "-r", apkPath)
	out, err := exec.Command("wsl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb install failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) Screenshot() ([]byte, error) {
	// Take screenshot on device
	args := c.adbArgs("shell", "screencap", "-p", "/sdcard/screenshot.png")
	if err := exec.Command("wsl", args...).Run(); err != nil {
		return nil, fmt.Errorf("screencap failed: %w", err)
	}

	// Pull screenshot
	args = c.adbArgs("pull", "/sdcard/screenshot.png", "/tmp/screenshot.png")
	if err := exec.Command("wsl", args...).Run(); err != nil {
		return nil, fmt.Errorf("screenshot pull failed: %w", err)
	}

	// Read from WSL
	args = c.wslArgs("cat", "/tmp/screenshot.png")
	out, err := exec.Command("wsl", args...).Output()
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
	out, err := exec.Command("wsl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("logcat failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) IsConnected() bool {
	args := c.wslArgs("adb", "devices")
	out, err := exec.Command("wsl", args...).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), c.target)
}

func (c *Client) Push(localPath, remotePath string) (string, error) {
	args := c.adbArgs("push", localPath, remotePath)
	out, err := exec.Command("wsl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb push failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c *Client) GetProp(prop string) (string, error) {
	return c.Run("getprop " + prop)
}
