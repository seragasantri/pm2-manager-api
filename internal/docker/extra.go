package docker

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/pkg/stdcopy"
)

// PortMap adalah satu pemetaan port container.
type PortMap struct {
	IP          string `json:"ip,omitempty"`
	PrivatePort uint16 `json:"privatePort"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
	Type        string `json:"type"`
}

// ContainerDetail adalah baris container untuk halaman Docker.
type ContainerDetail struct {
	ID             string            `json:"id"`
	ShortID        string            `json:"shortId"`
	Name           string            `json:"name"`
	Image          string            `json:"image"`
	State          string            `json:"state"`
	Status         string            `json:"status"`
	Created        int64             `json:"created"`
	Ports          []PortMap         `json:"ports"`
	Networks       []string          `json:"networks"`
	ComposeProject string            `json:"composeProject,omitempty"`
	ComposeService string            `json:"composeService,omitempty"`
	ComposeDir     string            `json:"composeDir,omitempty"`
	Mounts         int               `json:"mounts"`
	Labels         map[string]string `json:"-"`
	CPU            float64           `json:"cpu"`
	Memory         uint64            `json:"memory"`
	MemLimit       uint64            `json:"memLimit"`
}

// ListDetailed mengembalikan semua container dengan port, network & label compose.
func (c *Client) ListDetailed(ctx context.Context) ([]ContainerDetail, error) {
	cs, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]ContainerDetail, 0, len(cs))
	for _, ctr := range cs {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		d := ContainerDetail{
			ID: ctr.ID, ShortID: short(ctr.ID), Name: name, Image: ctr.Image,
			State: ctr.State, Status: ctr.Status, Created: ctr.Created,
			Mounts: len(ctr.Mounts), Labels: ctr.Labels,
			ComposeProject: ctr.Labels["com.docker.compose.project"],
			ComposeService: ctr.Labels["com.docker.compose.service"],
			ComposeDir:     ctr.Labels["com.docker.compose.project.working_dir"],
		}
		seen := map[string]bool{}
		for _, p := range ctr.Ports {
			key := fmt.Sprintf("%d/%d/%s", p.PrivatePort, p.PublicPort, p.Type)
			if seen[key] {
				continue // IPv4 + IPv6 dobel
			}
			seen[key] = true
			d.Ports = append(d.Ports, PortMap{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type})
		}
		if ctr.NetworkSettings != nil {
			for n := range ctr.NetworkSettings.Networks {
				d.Networks = append(d.Networks, n)
			}
			sort.Strings(d.Networks)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Logs mengambil N baris log terakhir (stdout+stderr digabung, dengan timestamp).
func (c *Client) Logs(ctx context.Context, id string, tail int, timestamps bool) (string, error) {
	if tail <= 0 || tail > 5000 {
		tail = 300
	}
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	rc, err := c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: fmt.Sprint(tail), Timestamps: timestamps,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if info.Config != nil && info.Config.Tty {
		_, err = buf.ReadFrom(rc)
	} else {
		_, err = stdcopy.StdCopy(&buf, &buf, rc)
	}
	if err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

// ContainerInspectView adalah ringkasan inspect yang relevan untuk UI.
type ContainerInspectView struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	ImageID       string            `json:"imageId"`
	Created       string            `json:"created"`
	StartedAt     string            `json:"startedAt"`
	FinishedAt    string            `json:"finishedAt"`
	State         string            `json:"state"`
	ExitCode      int               `json:"exitCode"`
	RestartCount  int               `json:"restartCount"`
	RestartPolicy string            `json:"restartPolicy"`
	Health        string            `json:"health,omitempty"`
	Cmd           []string          `json:"cmd"`
	Entrypoint    []string          `json:"entrypoint"`
	WorkingDir    string            `json:"workingDir"`
	Env           []string          `json:"env"`
	Mounts        []MountView       `json:"mounts"`
	Networks      map[string]string `json:"networks"`
	Labels        map[string]string `json:"labels"`
	Hostname      string            `json:"hostname"`
	NetworkMode   string            `json:"networkMode"`
}

// MountView ringkas satu mount.
type MountView struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

func (c *Client) InspectView(ctx context.Context, id string) (*ContainerInspectView, error) {
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	v := &ContainerInspectView{
		ID: info.ID, Name: strings.TrimPrefix(info.Name, "/"), ImageID: short(info.Image),
		Created: info.Created, Networks: map[string]string{},
	}
	if info.Config != nil {
		v.Image = info.Config.Image
		v.Cmd = info.Config.Cmd
		v.Entrypoint = info.Config.Entrypoint
		v.WorkingDir = info.Config.WorkingDir
		v.Env = info.Config.Env
		v.Labels = info.Config.Labels
		v.Hostname = info.Config.Hostname
	}
	if info.State != nil {
		v.State = info.State.Status
		v.ExitCode = info.State.ExitCode
		v.StartedAt = info.State.StartedAt
		v.FinishedAt = info.State.FinishedAt
		if info.State.Health != nil {
			v.Health = info.State.Health.Status
		}
	}
	v.RestartCount = info.RestartCount
	if info.HostConfig != nil {
		v.RestartPolicy = string(info.HostConfig.RestartPolicy.Name)
		v.NetworkMode = string(info.HostConfig.NetworkMode)
	}
	for _, m := range info.Mounts {
		v.Mounts = append(v.Mounts, MountView{Type: string(m.Type), Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	if info.NetworkSettings != nil {
		for name, n := range info.NetworkSettings.Networks {
			if n != nil {
				v.Networks[name] = n.IPAddress
			}
		}
	}
	return v, nil
}

// ExtAction menjalankan aksi container yang lebih lengkap.
func (c *Client) ExtAction(ctx context.Context, id, action string) error {
	switch action {
	case "start", "stop", "restart":
		return c.Action(ctx, id, action)
	case "pause":
		return c.cli.ContainerPause(ctx, id)
	case "unpause":
		return c.cli.ContainerUnpause(ctx, id)
	case "kill":
		return c.cli.ContainerKill(ctx, id, "SIGKILL")
	}
	return fmt.Errorf("aksi docker tidak dikenal: %s", action)
}

// ImageView ringkas satu image.
type ImageView struct {
	ID         string   `json:"id"`
	Tags       []string `json:"tags"`
	Size       int64    `json:"size"`
	Created    int64    `json:"created"`
	Containers int64    `json:"containers"`
	Dangling   bool     `json:"dangling"`
}

func (c *Client) Images(ctx context.Context) ([]ImageView, error) {
	imgs, err := c.cli.ImageList(ctx, image.ListOptions{All: false})
	if err != nil {
		return nil, err
	}
	used := map[string]int64{}
	if cs, err := c.cli.ContainerList(ctx, container.ListOptions{All: true}); err == nil {
		for _, ctr := range cs {
			used[ctr.ImageID]++
		}
	}
	out := make([]ImageView, 0, len(imgs))
	for _, im := range imgs {
		tags := im.RepoTags
		dangling := len(tags) == 0 || (len(tags) == 1 && tags[0] == "<none>:<none>")
		if dangling {
			tags = []string{}
		}
		out = append(out, ImageView{
			ID: short(im.ID), Tags: tags, Size: im.Size, Created: im.Created,
			Containers: used[im.ID], Dangling: dangling,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

// EngineInfo ringkas info daemon Docker.
type EngineInfo struct {
	ServerVersion     string `json:"serverVersion"`
	OS                string `json:"os"`
	Kernel            string `json:"kernel"`
	Architecture      string `json:"architecture"`
	CPUs              int    `json:"cpus"`
	MemTotal          int64  `json:"memTotal"`
	StorageDriver     string `json:"storageDriver"`
	Containers        int    `json:"containers"`
	ContainersRunning int    `json:"containersRunning"`
	ContainersPaused  int    `json:"containersPaused"`
	ContainersStopped int    `json:"containersStopped"`
	Images            int    `json:"images"`
	DockerRootDir     string `json:"dockerRootDir"`
}

func (c *Client) EngineInfo(ctx context.Context) (*EngineInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	i, err := c.cli.Info(ctx)
	if err != nil {
		return nil, err
	}
	return &EngineInfo{
		ServerVersion: i.ServerVersion, OS: i.OperatingSystem, Kernel: i.KernelVersion,
		Architecture: i.Architecture, CPUs: i.NCPU, MemTotal: i.MemTotal,
		StorageDriver: i.Driver, Containers: i.Containers, ContainersRunning: i.ContainersRunning,
		ContainersPaused: i.ContainersPaused, ContainersStopped: i.ContainersStopped,
		Images: i.Images, DockerRootDir: i.DockerRootDir,
	}, nil
}
