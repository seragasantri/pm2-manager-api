package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Client is a thin wrapper around the Docker SDK with Unix socket transport.
type Client struct {
	cli *client.Client
}

func New(socketPath string) (*Client, error) {
	cli, err := client.NewClientWithOpts(client.WithHost("unix://"+socketPath), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

// Container is a small summary used by app listing.
type Container struct {
	ID      string
	Name    string
	State   string
	Created int64
	Image   string
}

// List returns all containers (running and stopped).
func (c *Client) List(ctx context.Context) ([]Container, error) {
	cs, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(cs))
	for _, ctr := range cs {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		out = append(out, Container{
			ID:      ctr.ID,
			Name:    name,
			State:   ctr.State,
			Created: ctr.Created,
			Image:   ctr.Image,
		})
	}
	return out, nil
}

// Get returns a single container by name (exact match, stripping leading slash).
func (c *Client) Get(ctx context.Context, name string) (*Container, error) {
	cs, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i], nil
		}
	}
	return nil, nil
}

// CWD tries to resolve the "working directory" label on the container, defaulting to /app.
func (c *Client) CWD(ctx context.Context, id string) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	if cwd := info.Config.Labels["app.cwd"]; cwd != "" {
		return cwd, nil
	}
	if info.Config.WorkingDir != "" {
		return info.Config.WorkingDir, nil
	}
	return "/app", nil
}

// Action runs start/stop/restart on the container.
func (c *Client) Action(ctx context.Context, id, action string) error {
	switch action {
	case "start":
		return c.cli.ContainerStart(ctx, id, container.StartOptions{})
	case "stop":
		return c.cli.ContainerStop(ctx, id, container.StopOptions{})
	case "restart":
		return c.cli.ContainerRestart(ctx, id, container.StopOptions{})
	default:
		return fmt.Errorf("unsupported docker action: %s", action)
	}
}

// Exec runs a command inside the container and returns combined stdout/stderr.
func (c *Client) Exec(ctx context.Context, id, cmd string) (string, error) {
	execCfg := container.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
	}
	resp, err := c.cli.ContainerExecCreate(ctx, id, execCfg)
	if err != nil {
		return "", err
	}
	attach, err := c.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer attach.Close()

	var out bytes.Buffer
	if _, err := io.Copy(&out, attach.Reader); err != nil {
		return "", err
	}
	// Strip Docker multiplexed stream header (8 bytes per frame).
	return stripDockerStreamHeader(out.Bytes()), nil
}

// ExecStream runs `cmd` inside the container, streaming stdout/stderr frame-by-frame to w.
// Returns the exit code once the process completes.
func (c *Client) ExecStream(ctx context.Context, id, cmd string, w io.Writer) (int, error) {
	resp, err := c.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return -1, err
	}
	attach, err := c.cli.ContainerExecAttach(ctx, resp.ID, container.ExecAttachOptions{})
	if err != nil {
		return -1, err
	}
	defer attach.Close()

	if err := copyDockerStream(attach.Reader, w); err != nil && !errors.Is(err, io.EOF) {
		return -1, err
	}

	// Wait for exec to exit and return code.
	for {
		info, err := c.cli.ContainerExecInspect(ctx, resp.ID)
		if err != nil {
			return -1, err
		}
		if !info.Running {
			return info.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func copyDockerStream(r io.Reader, w io.Writer) error {
	header := make([]byte, 8)
	for {
		_, err := io.ReadFull(r, header)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		size := int(uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7]))
		if size == 0 {
			continue
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if _, err := w.Write(buf); err != nil {
			return err
		}
	}
}

func stripDockerStreamHeader(b []byte) string {
	// Each frame begins with [stream_type(1) | 0 0 0 | size(4)] then payload.
	// We keep all payloads; consumers can ignore stream type.
	var out bytes.Buffer
	for len(b) > 8 {
		size := int(uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7]))
		if size < 0 || 8+size > len(b) {
			break
		}
		out.Write(b[8 : 8+size])
		b = b[8+size:]
	}
	return out.String()
}
