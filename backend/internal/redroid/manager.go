package redroid

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	containerPrefix = "seeker-redroid-"
	imageRef        = "redroid/redroid:14.0.0_64only-latest"
	adbWaitTimeout  = 45 * time.Second
	adbPollInterval = 2 * time.Second
)

// ContainerInfo holds metadata about a managed container.
type ContainerInfo struct {
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container_name"`
	ADBPort       int       `json:"adb_port"`
	SessionID     int64     `json:"session_id"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"created_at"`
}

// Manager handles the full lifecycle of Redroid containers.
type Manager struct {
	client        *client.Client
	portAlloc     *PortAllocator
	maxContainers int
	gradleCache   string
	androidCache  string
}

// NewManager creates a container manager backed by the Docker daemon.
func NewManager(portAlloc *PortAllocator, maxContainers int) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client init: %w", err)
	}

	// Verify Docker connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("docker ping failed: %w", err)
	}

	// Initialize shared build caches
	homeDir, _ := os.UserHomeDir()
	gradleCache := filepath.Join(homeDir, ".seeker-studio", "gradle-cache")
	androidCache := filepath.Join(homeDir, ".seeker-studio", "android-cache")
	os.MkdirAll(gradleCache, 0755)
	os.MkdirAll(androidCache, 0755)

	return &Manager{
		client:        cli,
		portAlloc:     portAlloc,
		maxContainers: maxContainers,
		gradleCache:   gradleCache,
		androidCache:  androidCache,
	}, nil
}

// CreateContainer provisions a new Redroid container for a session.
// It allocates a port, starts the container, and waits for ADB readiness.
func (m *Manager) CreateContainer(sessionID int64) (*ContainerInfo, error) {
	// Check capacity — count existing running containers
	containers, err := m.ListContainers()
	if err != nil {
		return nil, fmt.Errorf("failed to list containers for capacity check: %w", err)
	}
	running := 0
	for _, c := range containers {
		if c.State == "running" {
			running++
		}
	}
	if running >= m.maxContainers {
		return nil, fmt.Errorf("host at capacity (%d/%d containers)", running, m.maxContainers)
	}

	// Allocate ADB port
	port, err := m.portAlloc.Allocate(sessionID)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s%d", containerPrefix, sessionID)

	// Remove any existing container with this name (idempotent)
	m.forceRemoveByName(name)

	// Configure container
	exposedPorts := nat.PortSet{"5555/tcp": struct{}{}}
	portBindings := nat.PortMap{
		"5555/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: fmt.Sprintf("%d", port)}},
	}

	containerCfg := &container.Config{
		Image: imageRef,
		Cmd: []string{
			"androidboot.use_memfd=1",
			"androidboot.redroid_width=1080",
			"androidboot.redroid_height=2400",
			"androidboot.redroid_dpi=420",
		},
		ExposedPorts: exposedPorts,
	}

	hostCfg := &container.HostConfig{
		Privileged:  true,
		PortBindings: portBindings,
		Resources: container.Resources{
			Memory:   1 << 30, // 1GB
			NanoCPUs: 1e9,     // 1 CPU
		},
		RestartPolicy: container.RestartPolicy{
			Name: "no",
		},
		Binds: []string{
			// Shared Gradle cache — persists across rebuilds for faster builds
			m.gradleCache + ":/root/.gradle/caches",
			// Shared Android SDK cache — persists downloaded SDK components
			m.androidCache + ":/root/.android/cache",
		},
	}

	ctx := context.Background()

	// Create
	resp, err := m.client.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, name)
	if err != nil {
		m.portAlloc.ReleasePort(port)
		return nil, fmt.Errorf("container create: %w", err)
	}
	containerID := resp.ID

	// Start
	if err := m.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		m.forceRemove(containerID)
		m.portAlloc.ReleasePort(port)
		return nil, fmt.Errorf("container start: %w", err)
	}

	// Wait for ADB readiness
	if err := m.waitForADB(port); err != nil {
		m.forceRemove(containerID)
		m.portAlloc.ReleasePort(port)
		return nil, fmt.Errorf("adb not ready: %w", err)
	}

	info := &ContainerInfo{
		ContainerID:   containerID,
		ContainerName: name,
		ADBPort:       port,
		SessionID:     sessionID,
		State:         "running",
		CreatedAt:     time.Now(),
	}

	log.Printf("container %s created for session %d (port %d)", containerID[:12], sessionID, port)
	return info, nil
}

// StopContainer gracefully stops and removes a container.
func (m *Manager) StopContainer(containerID string) error {
	ctx := context.Background()

	timeoutSecs := 10
	if err := m.client.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeoutSecs}); err != nil {
		log.Printf("WARN: container stop failed for %s: %v", containerID[:12], err)
	}

	if err := m.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("container remove: %w", err)
	}

	log.Printf("container %s stopped and removed", containerID[:12])
	return nil
}

// ForceRemove removes a container regardless of state.
func (m *Manager) ForceRemove(containerID string) {
	m.forceRemove(containerID)
}

func (m *Manager) forceRemove(containerID string) {
	ctx := context.Background()
	if err := m.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		log.Printf("WARN: force remove failed for %s: %v", containerID[:12], err)
	}
}

func (m *Manager) forceRemoveByName(name string) {
	ctx := context.Background()
	opts := container.RemoveOptions{Force: true}
	if err := m.client.ContainerRemove(ctx, name, opts); err != nil {
		// Not found is fine — that's the expected case
		if !client.IsErrNotFound(err) {
			log.Printf("WARN: remove by name %s failed: %v", name, err)
		}
	}
}

// PauseContainer pauses a running container.
func (m *Manager) PauseContainer(containerID string) error {
	ctx := context.Background()
	if err := m.client.ContainerPause(ctx, containerID); err != nil {
		return fmt.Errorf("container pause: %w", err)
	}
	log.Printf("container %s paused", containerID[:12])
	return nil
}

// UnpauseContainer resumes a paused container.
func (m *Manager) UnpauseContainer(containerID string) error {
	ctx := context.Background()
	if err := m.client.ContainerUnpause(ctx, containerID); err != nil {
		return fmt.Errorf("container unpause: %w", err)
	}
	log.Printf("container %s resumed", containerID[:12])
	return nil
}

// HealthCheck returns true if the container is running.
func (m *Manager) HealthCheck(containerID string) (bool, error) {
	ctx := context.Background()
	info, err := m.client.ContainerInspect(ctx, containerID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return info.State != nil && info.State.Running, nil
}

// ListContainers returns all seeker-redroid-* containers managed by this system.
func (m *Manager) ListContainers() ([]ContainerInfo, error) {
	ctx := context.Background()
	filter := filters.NewArgs()
	filter.Add("name", containerPrefix)

	containers, err := m.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filter,
	})
	if err != nil {
		return nil, fmt.Errorf("container list: %w", err)
	}

	var result []ContainerInfo
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		// Extract session ID from name
		var sessionID int64
		fmt.Sscanf(name, containerPrefix+"%d", &sessionID)

		// Extract ADB port from port bindings
		var adbPort int
		for _, p := range c.Ports {
			if p.PrivatePort == 5555 {
				adbPort = int(p.PublicPort)
				break
			}
		}

		state := strings.ToLower(c.State)

		result = append(result, ContainerInfo{
			ContainerID:   c.ID,
			ContainerName: name,
			ADBPort:       adbPort,
			SessionID:     sessionID,
			State:         state,
			CreatedAt:     time.Unix(c.Created, 0),
		})
	}

	return result, nil
}

// waitForADB polls until ADB can connect to the given port.
func (m *Manager) waitForADB(port int) error {
	deadline := time.Now().Add(adbWaitTimeout)
	target := fmt.Sprintf("localhost:%d", port)

	for time.Now().Before(deadline) {
		out, err := exec.Command("adb", "devices").CombinedOutput()
		if err == nil && strings.Contains(string(out), target) {
			// Try to connect explicitly
			connectOut, _ := exec.Command("adb", "connect", target).CombinedOutput()
			if strings.Contains(string(connectOut), "connected") || strings.Contains(string(connectOut), "already") {
				return nil
			}
		}

		// Also try direct connect
		exec.Command("adb", "connect", target).Run()

		time.Sleep(adbPollInterval)
	}

	return fmt.Errorf("ADB not reachable at %s after %v", target, adbWaitTimeout)
}

// GetContainerID returns the container ID for a session, or empty string.
func (m *Manager) GetContainerID(sessionID int64) string {
	containers, err := m.ListContainers()
	if err != nil {
		return ""
	}
	for _, c := range containers {
		if c.SessionID == sessionID {
			return c.ContainerID
		}
	}
	return ""
}
