package dockerapi

// Version is the daemon's reported version info.
type Version struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
}

// Container is a summary entry from /containers/json.
type Container struct {
	ID              string            `json:"Id"`
	Names           []string          `json:"Names"`
	Image           string            `json:"Image"`
	ImageID         string            `json:"ImageID"`
	Command         string            `json:"Command"`
	Created         int64             `json:"Created"`
	State           string            `json:"State"`
	Status          string            `json:"Status"`
	Labels          map[string]string `json:"Labels"`
	NetworkSettings *struct {
		Networks map[string]EndpointSettings `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Name returns the container's primary name without the leading slash.
func (c Container) Name() string {
	if len(c.Names) == 0 {
		return ""
	}
	return trimSlash(c.Names[0])
}

func trimSlash(s string) string {
	if len(s) > 0 && s[0] == '/' {
		return s[1:]
	}
	return s
}

// EndpointSettings is a container's attachment to one network.
type EndpointSettings struct {
	NetworkID  string   `json:"NetworkID,omitempty"`
	EndpointID string   `json:"EndpointID,omitempty"`
	IPAddress  string   `json:"IPAddress,omitempty"`
	Gateway    string   `json:"Gateway,omitempty"`
	Aliases    []string `json:"Aliases,omitempty"`
}

// ContainerInspect is the full /containers/{id}/json payload publix reads.
type ContainerInspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Image   string `json:"Image"`
	// RestartCount is how many times Docker has restarted this container.
	// It is the only reliable way to tell a slow starter apart from one
	// that is crash-looping, since a restarting container looks alive.
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		Dead       bool   `json:"Dead"`
		Pid        int    `json:"Pid"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
		} `json:"Health,omitempty"`
	} `json:"State"`
	Config *struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
		Cmd    []string          `json:"Cmd"`
	} `json:"Config"`
	NetworkSettings struct {
		IPAddress string                      `json:"IPAddress"`
		Networks  map[string]EndpointSettings `json:"Networks"`
	} `json:"NetworkSettings"`
}

// IPOn returns the container's address on the named network, falling back to
// any attached network so probing still works on non-standard setups.
func (ci *ContainerInspect) IPOn(network string) string {
	if e, ok := ci.NetworkSettings.Networks[network]; ok && e.IPAddress != "" {
		return e.IPAddress
	}
	for _, e := range ci.NetworkSettings.Networks {
		if e.IPAddress != "" {
			return e.IPAddress
		}
	}
	return ci.NetworkSettings.IPAddress
}

// CreateConfig is the container-create request body.
type CreateConfig struct {
	Image            string              `json:"Image"`
	Cmd              []string            `json:"Cmd,omitempty"`
	Env              []string            `json:"Env,omitempty"`
	Labels           map[string]string   `json:"Labels,omitempty"`
	User             string              `json:"User,omitempty"`
	StopSignal       string              `json:"StopSignal,omitempty"`
	StopTimeout      *int                `json:"StopTimeout,omitempty"`
	ExposedPorts     map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig       *HostConfig         `json:"HostConfig,omitempty"`
	NetworkingConfig *NetworkingConfig   `json:"NetworkingConfig,omitempty"`
}

// HostConfig carries the host-side runtime settings.
type HostConfig struct {
	Binds             []string       `json:"Binds,omitempty"`
	NetworkMode       string         `json:"NetworkMode,omitempty"`
	RestartPolicy     *RestartPolicy `json:"RestartPolicy,omitempty"`
	Memory            int64          `json:"Memory,omitempty"`
	MemoryReservation int64          `json:"MemoryReservation,omitempty"`
	NanoCPUs          int64          `json:"NanoCpus,omitempty"`
	PidsLimit         *int64         `json:"PidsLimit,omitempty"`
	Init              *bool          `json:"Init,omitempty"`
	AutoRemove        bool           `json:"AutoRemove,omitempty"`
	LogConfig         *LogConfig     `json:"LogConfig,omitempty"`
	Mounts            []Mount        `json:"Mounts,omitempty"`
}

// RestartPolicy tells Docker how to react to a container exiting.
type RestartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount,omitempty"`
}

// LogConfig selects the container log driver and its options.
type LogConfig struct {
	Type   string            `json:"Type,omitempty"`
	Config map[string]string `json:"Config,omitempty"`
}

// Mount is a volume or bind mount.
type Mount struct {
	Type     string `json:"Type"`
	Source   string `json:"Source"`
	Target   string `json:"Target"`
	ReadOnly bool   `json:"ReadOnly,omitempty"`
}

// NetworkingConfig attaches a container to networks at create time.
type NetworkingConfig struct {
	EndpointsConfig map[string]*EndpointSettings `json:"EndpointsConfig,omitempty"`
}

// CreateResponse is returned by /containers/create.
type CreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// Network is a summary entry from /networks.
type Network struct {
	Name   string            `json:"Name"`
	ID     string            `json:"Id"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels"`
}

// Image is a summary entry from /images/json.
type Image struct {
	ID       string            `json:"Id"`
	RepoTags []string          `json:"RepoTags"`
	Created  int64             `json:"Created"`
	Size     int64             `json:"Size"`
	Labels   map[string]string `json:"Labels"`
}

// Volume is a summary entry from /volumes.
type Volume struct {
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels"`
}

// ExecResult is the outcome of running a command inside a container.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}
