package dockerapi

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ListContainers returns containers matching the given label selectors.
// all includes stopped containers.
func (c *Client) ListContainers(ctx context.Context, all bool, labels ...string) ([]Container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	if len(labels) > 0 {
		q.Set("filters", filterArgs(map[string][]string{"label": labels}))
	}
	var out []Container
	if err := c.getJSON(ctx, "/containers/json", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectContainer returns the full state of one container.
func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerInspect, error) {
	var out ContainerInspect
	if err := c.getJSON(ctx, "/containers/"+id+"/json", nil, &out); err != nil {
		return nil, err
	}
	out.Name = trimSlash(out.Name)
	return &out, nil
}

// CreateContainer creates a container with the given name and config.
func (c *Client) CreateContainer(ctx context.Context, name string, cfg *CreateConfig) (*CreateResponse, error) {
	q := url.Values{"name": {name}}
	var out CreateResponse
	if err := c.postJSON(ctx, "/containers/create", q, cfg, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartContainer starts a created container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.postJSON(ctx, "/containers/"+id+"/start", nil, nil, nil)
}

// StopContainer stops a container, giving it timeout seconds to exit cleanly.
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout.Seconds()))}}
	err := c.postJSON(ctx, "/containers/"+id+"/stop", q, nil, nil)
	// 304 means "already stopped", which is the state we wanted anyway.
	var ae *APIError
	if asAPIError(err, &ae) && (ae.Status == http.StatusNotModified || ae.Status == http.StatusNotFound) {
		return nil
	}
	return err
}

// RemoveContainer deletes a container, optionally forcing it and its volumes.
func (c *Client) RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if volumes {
		q.Set("v", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/containers/"+id, q, nil)
	if err != nil {
		if NotFound(err) {
			return nil
		}
		return err
	}
	resp.Body.Close()
	return nil
}

// WaitContainer blocks until the container exits and returns its exit code.
func (c *Client) WaitContainer(ctx context.Context, id string) (int, error) {
	var out struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := c.postJSON(ctx, "/containers/"+id+"/wait", nil, nil, &out); err != nil {
		return -1, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return out.StatusCode, fmt.Errorf("%s", out.Error.Message)
	}
	return out.StatusCode, nil
}

// RenameContainer changes a container's name.
func (c *Client) RenameContainer(ctx context.Context, id, name string) error {
	return c.postJSON(ctx, "/containers/"+id+"/rename", url.Values{"name": {name}}, nil, nil)
}

// Exec runs a command inside a running container and collects its output.
func (c *Client) Exec(ctx context.Context, id string, cmd []string) (*ExecResult, error) {
	var created struct {
		ID string `json:"Id"`
	}
	body := map[string]any{
		"AttachStdout": true,
		"AttachStderr": true,
		"Cmd":          cmd,
	}
	if err := c.postJSON(ctx, "/containers/"+id+"/exec", nil, body, &created); err != nil {
		return nil, err
	}

	resp, err := c.do(ctx, http.MethodPost, "/exec/"+created.ID+"/start",
		nil, map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	stdout, stderr, err := demux(resp.Body)
	if err != nil {
		return nil, err
	}

	var info struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	// The exit code is only final once the exec has stopped running.
	for i := 0; i < 50; i++ {
		if err := c.getJSON(ctx, "/exec/"+created.ID+"/json", nil, &info); err != nil {
			return nil, err
		}
		if !info.Running {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return &ExecResult{ExitCode: info.ExitCode, Stdout: stdout, Stderr: stderr}, nil
}

// demux splits Docker's multiplexed stdout/stderr stream framing.
func demux(r io.Reader) (stdout, stderr string, err error) {
	var so, se strings.Builder
	br := bufio.NewReader(r)
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return so.String(), se.String(), nil
			}
			return so.String(), se.String(), err
		}
		n := binary.BigEndian.Uint32(hdr[4:8])
		if n > 8<<20 {
			return so.String(), se.String(), fmt.Errorf("oversized docker stream frame (%d bytes)", n)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			return so.String(), se.String(), nil
		}
		if hdr[0] == 2 {
			se.Write(buf)
		} else {
			so.Write(buf)
		}
	}
}

// LogOptions selects which container logs to stream.
type LogOptions struct {
	Stdout     bool
	Stderr     bool
	Follow     bool
	Timestamps bool
	Tail       string
	Since      time.Time
}

// ContainerLogs streams a container's logs, demultiplexing Docker's frame
// format and writing plain lines to w. It returns when the stream ends or
// ctx is cancelled.
func (c *Client) ContainerLogs(ctx context.Context, id string, opt LogOptions, w io.Writer) error {
	q := url.Values{}
	if opt.Stdout {
		q.Set("stdout", "1")
	}
	if opt.Stderr {
		q.Set("stderr", "1")
	}
	if opt.Follow {
		q.Set("follow", "1")
	}
	if opt.Timestamps {
		q.Set("timestamps", "1")
	}
	if opt.Tail != "" {
		q.Set("tail", opt.Tail)
	}
	if !opt.Since.IsZero() {
		q.Set("since", strconv.FormatInt(opt.Since.Unix(), 10))
	}

	resp, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/logs", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return copyDemux(ctx, resp.Body, w)
}

// copyDemux forwards a multiplexed docker stream to w as plain bytes.
func copyDemux(ctx context.Context, r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	hdr := make([]byte, 8)
	for {
		if ctx.Err() != nil {
			return nil
		}
		if _, err := io.ReadFull(br, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		// A container started with a TTY sends raw bytes with no framing.
		// Detect that by an implausible stream byte and fall back to a copy.
		if hdr[0] > 2 {
			if _, err := w.Write(hdr); err != nil {
				return err
			}
			_, err := io.Copy(w, br)
			return err
		}
		n := binary.BigEndian.Uint32(hdr[4:8])
		if n > 8<<20 {
			return fmt.Errorf("oversized docker log frame (%d bytes)", n)
		}
		if _, err := io.CopyN(w, br, int64(n)); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// Event is one entry from the docker event stream.
type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time int64 `json:"time"`
}

// Events streams docker events matching the given label selectors until ctx
// is cancelled, invoking fn for each one.
func (c *Client) Events(ctx context.Context, labels []string, fn func(Event)) error {
	q := url.Values{}
	f := map[string][]string{"type": {"container"}}
	if len(labels) > 0 {
		f["label"] = labels
	}
	q.Set("filters", filterArgs(f))

	resp, err := c.do(ctx, http.MethodGet, "/events", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			if ctx.Err() != nil || err == io.EOF {
				return nil
			}
			return err
		}
		fn(ev)
	}
}

// Stats is a point-in-time resource sample for one container.
type Stats struct {
	CPUPercent float64
	MemUsage   int64
	MemLimit   int64
}

// ContainerStats takes a single non-streaming resource sample.
func (c *Client) ContainerStats(ctx context.Context, id string) (*Stats, error) {
	var raw struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage  int64   `json:"total_usage"`
				PercpuUsage []int64 `json:"percpu_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage int64 `json:"system_cpu_usage"`
			OnlineCPUs     int   `json:"online_cpus"`
		} `json:"cpu_stats"`
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage int64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage int64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		MemoryStats struct {
			Usage int64 `json:"usage"`
			Limit int64 `json:"limit"`
			Stats struct {
				Cache        int64 `json:"cache"`
				InactiveFile int64 `json:"inactive_file"`
			} `json:"stats"`
		} `json:"memory_stats"`
	}
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	if err := c.getJSON(ctx, "/containers/"+id+"/stats", q, &raw); err != nil {
		return nil, err
	}

	s := &Stats{MemLimit: raw.MemoryStats.Limit}
	// Match `docker stats` and exclude reclaimable page cache from usage.
	s.MemUsage = raw.MemoryStats.Usage - raw.MemoryStats.Stats.InactiveFile
	if s.MemUsage < 0 {
		s.MemUsage = raw.MemoryStats.Usage
	}

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	cpus := raw.CPUStats.OnlineCPUs
	if cpus == 0 {
		cpus = len(raw.CPUStats.CPUUsage.PercpuUsage)
	}
	if cpuDelta > 0 && sysDelta > 0 && cpus > 0 {
		s.CPUPercent = (cpuDelta / sysDelta) * float64(cpus) * 100
	}
	return s, nil
}
