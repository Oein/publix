// Package compose reads Compose files and drives the `docker compose` CLI.
//
// publix does not reimplement Compose. A repository that ships a compose
// file has already described its stack precisely, often with dependencies,
// healthchecks and startup ordering that took real effort to get right.
// publix reads that file to learn the stack's shape, then layers its own
// concerns on with a generated override file and lets Compose do the work.
package compose

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the subset of the Compose specification publix needs to read.
// Anything it does not model is passed through to Compose untouched.
type File struct {
	Name     string             `yaml:"name,omitempty"`
	Services map[string]Service `yaml:"services"`
	Volumes  map[string]any     `yaml:"volumes,omitempty"`
	Networks map[string]any     `yaml:"networks,omitempty"`
}

// Service is one service in a compose file.
type Service struct {
	Image       string   `yaml:"image,omitempty"`
	Build       any      `yaml:"build,omitempty"`
	Ports       []any    `yaml:"ports,omitempty"`
	Expose      []any    `yaml:"expose,omitempty"`
	Environment any      `yaml:"environment,omitempty"`
	DependsOn   any      `yaml:"depends_on,omitempty"`
	Restart     string   `yaml:"restart,omitempty"`
	Profiles    []string `yaml:"profiles,omitempty"`
	Labels      any      `yaml:"labels,omitempty"`
	Volumes     []any    `yaml:"volumes,omitempty"`
	Command     any      `yaml:"command,omitempty"`
}

// Parse reads a compose file. Unknown fields are tolerated: publix must not
// refuse to deploy a stack just because Compose grew a feature it has not
// heard of.
func Parse(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s is not a valid compose file: %w", path, err)
	}
	if len(f.Services) == 0 {
		return nil, fmt.Errorf("%s declares no services", path)
	}
	return &f, nil
}

// ServiceNames returns the services in a stable order.
func (f *File) ServiceNames() []string {
	out := make([]string, 0, len(f.Services))
	for n := range f.Services {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Guess picks the service most likely to be the one users reach, and the
// port it serves on.
//
// The heuristic is ordered by how strong a signal each thing is: a published
// port is near-certain, an exposed port is likely, and a conventional name
// is a last resort. A stack with exactly one service needs no guessing.
func Guess(path string) (service string, port int) {
	f, err := Parse(path)
	if err != nil {
		return "", 0
	}
	return f.Guess()
}

// Guess is the parsed form of the package-level Guess.
func (f *File) Guess() (service string, port int) {
	names := f.ServiceNames()

	if len(names) == 1 {
		s := f.Services[names[0]]
		return names[0], firstPort(s)
	}

	// A service that publishes a port to the host is almost always the
	// entrypoint of the stack.
	var published []string
	for _, n := range names {
		if len(f.Services[n].Ports) > 0 {
			published = append(published, n)
		}
	}
	if len(published) == 1 {
		return published[0], firstPort(f.Services[published[0]])
	}

	candidates := published
	if len(candidates) == 0 {
		candidates = names
	}
	// Prefer conventional web-tier names over infrastructure services.
	for _, want := range []string{"web", "app", "frontend", "www", "api", "server", "nginx", "caddy"} {
		for _, n := range candidates {
			if strings.EqualFold(n, want) {
				return n, firstPort(f.Services[n])
			}
		}
	}
	// Fall back to the first candidate that is not obviously a backing
	// service, so a stack of app+postgres+redis still resolves sensibly.
	for _, n := range candidates {
		if !isBackingService(n, f.Services[n].Image) {
			return n, firstPort(f.Services[n])
		}
	}
	if len(candidates) > 0 {
		return candidates[0], firstPort(f.Services[candidates[0]])
	}
	return "", 0
}

// backingImages are images that are never the public face of a stack.
var backingImages = []string{
	"postgres", "mysql", "mariadb", "mongo", "redis", "valkey", "memcached",
	"rabbitmq", "kafka", "zookeeper", "elasticsearch", "opensearch",
	"clickhouse", "minio", "influxdb", "prometheus", "grafana", "loki",
	"nats", "etcd", "cassandra", "neo4j", "meilisearch", "typesense", "qdrant",
}

func isBackingService(name, image string) bool {
	hay := strings.ToLower(name + " " + image)
	for _, b := range backingImages {
		if strings.Contains(hay, b) {
			return true
		}
	}
	return false
}

// firstPort extracts the container-side port a service listens on, from
// either its published ports or its expose list.
func firstPort(s Service) int {
	for _, p := range s.Ports {
		if n := containerPort(p); n > 0 {
			return n
		}
	}
	for _, e := range s.Expose {
		if n := toPort(e); n > 0 {
			return n
		}
	}
	return 0
}

// containerPort reads the container side of a port mapping, which may be a
// short-syntax string ("8080:80", "127.0.0.1:8080:80/tcp") or a long-syntax
// mapping ({target: 80, published: 8080}).
func containerPort(p any) int {
	switch v := p.(type) {
	case map[string]any:
		return toPort(v["target"])
	case string:
		s := v
		if i := strings.Index(s, "/"); i >= 0 { // drop /tcp, /udp
			s = s[:i]
		}
		parts := strings.Split(s, ":")
		// The container port is always the last field: "80",
		// "8080:80" and "127.0.0.1:8080:80" all end with it.
		last := parts[len(parts)-1]
		// A range ("8000-8010") has no single answer; take its start.
		if i := strings.Index(last, "-"); i > 0 {
			last = last[:i]
		}
		return toPort(last)
	default:
		return toPort(p)
	}
}

func toPort(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		p, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0
		}
		return p
	}
	return 0
}

// HasBuild reports whether any service builds from source, which tells the
// engine whether `docker compose up` needs a build step at all.
func (f *File) HasBuild() bool {
	for _, s := range f.Services {
		if s.Build != nil {
			return true
		}
	}
	return false
}
