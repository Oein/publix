package deployspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRepo builds a fake checkout on disk.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func resolve(t *testing.T, yaml string, files map[string]string) *Resolved {
	t.Helper()
	sp, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r, err := sp.Resolve(writeRepo(t, files))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return r
}

// The headline promise is that a repository with a Dockerfile needs almost
// nothing in deployment.yaml.
func TestMinimalDockerfileSpec(t *testing.T) {
	r := resolve(t, "name: api\ndomains: [api.example.com]\n", map[string]string{
		"Dockerfile": "FROM alpine\nEXPOSE 8080\n",
	})
	if r.Kind != KindDockerfile {
		t.Errorf("kind = %q, want dockerfile", r.Kind)
	}
	if r.Port != 8080 {
		t.Errorf("port = %d, want 8080 detected from EXPOSE", r.Port)
	}
	if r.ReplicaCount() != 1 {
		t.Errorf("replicas = %d, want 1", r.ReplicaCount())
	}
	if r.Release.Strategy != StrategyBlueGreen {
		t.Errorf("strategy = %q, want blue-green", r.Release.Strategy)
	}
	if len(r.Routes) != 1 || r.Routes[0].Domain != "api.example.com" {
		t.Errorf("routes = %+v, want the domain normalised into a route", r.Routes)
	}
}

// A compose repository must be detected, and must default to recreate:
// two generations of a compose stack cannot coexist.
func TestComposeDetectionAndStrategy(t *testing.T) {
	r := resolve(t, "", map[string]string{
		"docker-compose.yml": `
services:
  web:
    image: nginx
    ports: ["8080:80"]
  db:
    image: postgres:16
`,
	})
	if r.Kind != KindCompose {
		t.Fatalf("kind = %q, want compose", r.Kind)
	}
	if r.Compose != "docker-compose.yml" {
		t.Errorf("compose = %q", r.Compose)
	}
	if r.Service != "web" {
		t.Errorf("service = %q, want web (the only service publishing a port)", r.Service)
	}
	if r.Port != 80 {
		t.Errorf("port = %d, want the container side of 8080:80", r.Port)
	}
	if r.Release.Strategy != StrategyRecreate {
		t.Errorf("strategy = %q, want recreate for compose", r.Release.Strategy)
	}
}

// A compose file outranks a Dockerfile: it describes the whole stack.
func TestComposeOutranksDockerfile(t *testing.T) {
	r := resolve(t, "service: web\nport: 3000\n", map[string]string{
		"Dockerfile":   "FROM alpine\nEXPOSE 3000\n",
		"compose.yaml": "services:\n  web:\n    build: .\n",
	})
	if r.Kind != KindCompose {
		t.Errorf("kind = %q, want compose to win over a Dockerfile", r.Kind)
	}
}

func TestStaticDetectionFromVite(t *testing.T) {
	r := resolve(t, "", map[string]string{
		"package.json":      `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^5"}}`,
		"package-lock.json": "{}",
	})
	if r.Kind != KindStatic {
		t.Fatalf("kind = %q, want static", r.Kind)
	}
	if r.Build.Output != "dist" {
		t.Errorf("output = %q, want dist", r.Build.Output)
	}
	if r.Build.Install != "npm ci" {
		t.Errorf("install = %q, want npm ci from the lockfile", r.Build.Install)
	}
	if !r.Build.SPA {
		t.Error("SPA fallback should be on for a Vite app")
	}
	if r.Port != 80 {
		t.Errorf("port = %d, want 80", r.Port)
	}
}

// pnpm and yarn lockfiles must select their own install commands: running
// `npm ci` in a pnpm repo produces a subtly different dependency tree.
func TestInstallCommandFollowsLockfile(t *testing.T) {
	for lockfile, want := range map[string]string{
		"pnpm-lock.yaml":    "pnpm install --frozen-lockfile",
		"yarn.lock":         "yarn install --frozen-lockfile",
		"package-lock.json": "npm ci",
	} {
		r := resolve(t, "", map[string]string{
			"package.json": `{"devDependencies":{"vite":"^5"}}`,
			lockfile:       "",
		})
		if r.Build.Install != want {
			t.Errorf("%s: install = %q, want %q", lockfile, r.Build.Install, want)
		}
	}
}

// Every problem should be reported at once, not one per run.
func TestValidationReportsAllProblems(t *testing.T) {
	sp, err := Parse([]byte(`
name: "Not A Valid Name"
type: dockerfile
port: 99999
replicas: 0
domains: ["not a domain"]
resources:
  cpu: "-1"
  memory: "wat"
`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = sp.Resolve(writeRepo(t, nil))
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error is %T, want *ValidationError", err)
	}
	if len(ve.Problems) < 6 {
		t.Errorf("got %d problems, want every one reported at once:\n%s", len(ve.Problems), err)
	}
	for _, want := range []string{"name:", "port:", "replicas:", "routes[0].domain:", "resources.cpu:", "resources.memory:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%s", want, err)
		}
	}
}

// A subPath escaping the project's own directory would reach another
// project's data on the same shared volume.
func TestVolumeSubPathCannotEscape(t *testing.T) {
	for _, bad := range []string{"../other", "/etc", "a/../../b"} {
		sp, err := Parse([]byte("port: 80\nvolumes:\n  - name: disk0\n    subPath: \"" + bad + "\"\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = sp.Resolve(writeRepo(t, map[string]string{"Dockerfile": "FROM alpine\n"}))
		if err == nil || !strings.Contains(err.Error(), "subPath") {
			t.Errorf("subPath %q should be rejected, got: %v", bad, err)
		}
	}
}

func TestVolumeShorthandAndMountPath(t *testing.T) {
	r := resolve(t, `
port: 80
volumes:
  - disk0
  - name: disk1
    mountPath: /data
    readOnly: true
`, map[string]string{"Dockerfile": "FROM alpine\n"})

	if len(r.Volumes) != 2 {
		t.Fatalf("got %d volumes", len(r.Volumes))
	}
	if r.Volumes[0].Name != "disk0" || r.Volumes[0].Mount() != "/shared/disk0" {
		t.Errorf("bare name should mount at /shared/<name>, got %+v", r.Volumes[0])
	}
	if r.Volumes[1].Mount() != "/data" || !r.Volumes[1].ReadOnly {
		t.Errorf("explicit mapping not honoured: %+v", r.Volumes[1])
	}
}

func TestDuplicateMountPathRejected(t *testing.T) {
	sp, _ := Parse([]byte("port: 80\nvolumes:\n  - {name: a, mountPath: /data}\n  - {name: b, mountPath: /data}\n"))
	_, err := sp.Resolve(writeRepo(t, map[string]string{"Dockerfile": "FROM alpine\n"}))
	if err == nil || !strings.Contains(err.Error(), "more than one volume") {
		t.Errorf("two volumes on one mount path should be rejected, got: %v", err)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	// A typo in a key must be an error, not silently ignored: a spec that
	// says `domain:` instead of `domains:` would otherwise deploy with no
	// routing at all and look like it worked.
	_, err := Parse([]byte("naem: typo\n"))
	if err == nil {
		t.Fatal("an unknown field should be rejected")
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"512M": 512 << 20, "2G": 2 << 30, "1024": 1024,
		"1k": 1 << 10, "1KB": 1 << 10, "1.5G": 1610612736, "": 0,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"wat", "-5M", "1.2.3G"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should have failed", bad)
		}
	}
}

func TestEmptySpecIsValidForADockerfileRepo(t *testing.T) {
	r := resolve(t, "", map[string]string{"Dockerfile": "FROM nginx\nEXPOSE 80\n"})
	if r.Kind != KindDockerfile || r.Port != 80 {
		t.Errorf("an empty deployment.yaml should still resolve: kind=%q port=%d", r.Kind, r.Port)
	}
}
