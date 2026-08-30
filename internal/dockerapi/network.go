package dockerapi

import (
	"context"
	"net/http"
	"net/url"
)

// ListNetworks returns all docker networks.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var out []Network
	if err := c.getJSON(ctx, "/networks", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnsureNetwork creates an attachable bridge network if it does not exist,
// and returns its ID. Concurrent creation is tolerated.
func (c *Client) EnsureNetwork(ctx context.Context, name string, labels map[string]string) (string, error) {
	nets, err := c.ListNetworks(ctx)
	if err != nil {
		return "", err
	}
	for _, n := range nets {
		if n.Name == name {
			return n.ID, nil
		}
	}
	body := map[string]any{
		"Name":           name,
		"Driver":         "bridge",
		"CheckDuplicate": true,
		"Attachable":     true,
		"Labels":         labels,
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.postJSON(ctx, "/networks/create", nil, body, &out); err != nil {
		if Conflict(err) {
			// Another process won the race; look it up again.
			nets, lerr := c.ListNetworks(ctx)
			if lerr != nil {
				return "", err
			}
			for _, n := range nets {
				if n.Name == name {
					return n.ID, nil
				}
			}
		}
		return "", err
	}
	return out.ID, nil
}

// ConnectNetwork attaches a container to a network after creation.
func (c *Client) ConnectNetwork(ctx context.Context, network, container string, aliases []string) error {
	body := map[string]any{
		"Container":      container,
		"EndpointConfig": map[string]any{"Aliases": aliases},
	}
	err := c.postJSON(ctx, "/networks/"+url.PathEscape(network)+"/connect", nil, body, nil)
	if Conflict(err) {
		return nil // already attached
	}
	return err
}

// ListVolumes returns volumes matching the given label selectors.
func (c *Client) ListVolumes(ctx context.Context, labels ...string) ([]Volume, error) {
	q := url.Values{}
	if len(labels) > 0 {
		q.Set("filters", filterArgs(map[string][]string{"label": labels}))
	}
	var out struct {
		Volumes []Volume `json:"Volumes"`
	}
	if err := c.getJSON(ctx, "/volumes", q, &out); err != nil {
		return nil, err
	}
	return out.Volumes, nil
}

// EnsureVolume creates a named volume if it does not already exist.
func (c *Client) EnsureVolume(ctx context.Context, name string, labels map[string]string) error {
	err := c.getJSON(ctx, "/volumes/"+url.PathEscape(name), nil, nil)
	if err == nil {
		return nil
	}
	if !NotFound(err) {
		return err
	}
	body := map[string]any{"Name": name, "Labels": labels}
	return c.postJSON(ctx, "/volumes/create", nil, body, nil)
}

// RemoveVolume deletes a named volume.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), q, nil)
	if err != nil {
		if NotFound(err) {
			return nil
		}
		return err
	}
	resp.Body.Close()
	return nil
}
