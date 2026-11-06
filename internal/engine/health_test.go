package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/Oein/publix/internal/deployspec"
)

func TestProbePort(t *testing.T) {
	dc := &Context{
		Spec:       &deployspec.Resolved{Spec: &deployspec.Spec{Port: 3000}},
		ProbePorts: map[string]int{"backend": 8080, "frontend": 3000},
	}
	cases := []struct {
		name      string
		health    int
		container string
		want      int
	}{
		{"a recorded spec has the project's port filled in", 3000, "backend", 8080},
		{"an unset port uses the service's", 0, "backend", 8080},
		{"the routed service keeps its own", 3000, "frontend", 3000},
		{"a port chosen by hand wins", 9000, "backend", 9000},
		{"a container with no known port uses the project's", 3000, "worker", 3000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := probePort(dc, deployspec.Health{Port: c.health}, c.container); got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

// A redeploy reads the deployment's recorded spec, where health.port has been
// filled in. A stack whose services listen on different ports has to pass the
// health gate that way just as it did the first time.
func TestRedeployComposeProbesEachServiceOnItsOwnPort(t *testing.T) {
	h := newHarness(t)
	p := h.project("twoports", map[string]string{
		"deployment.yaml": strings.Join([]string{
			"type: compose",
			"service: web",
			"port: 80",
			"health:",
			"  type: tcp",
			"  grace: 30s",
			"routes:",
			"  - domain: api.test.local",
			"    service: api",
			"  - domain: web.test.local",
			"    service: web",
			"",
		}, "\n"),
		"docker-compose.yml": `
services:
  web:
    image: nginx:alpine
  api:
    image: busybox:1.37.0
    command: ["httpd", "-f", "-p", "8081"]
    expose:
      - "8081"
`,
	})
	h.mustSucceed(h.deploy(p, Options{Trigger: "test"}))

	again, err := h.engine.Redeploy(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.mustSucceed(h.await(p.ID, again.ID, 2*time.Minute))
}
