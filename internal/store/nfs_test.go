package store

import (
	"strings"
	"testing"
)

func nfsVolume(name string, scope VolumeScope) Volume {
	return Volume{
		Name:  name,
		Scope: scope,
		NFS:   &NFSMount{Server: "192.168.20.175", Export: "/mnt/hdd-4tb/data"},
	}
}

func TestValidateNFSVolume(t *testing.T) {
	s := &Settings{}
	if err := s.ValidateVolume(nfsVolume("data", ScopeProject), ""); err != nil {
		t.Fatalf("a volume with no host path must be valid when it has an export: %v", err)
	}

	cases := []struct {
		name string
		edit func(*NFSMount)
		want string
	}{
		{"no server", func(n *NFSMount) { n.Server = "" }, "server is required"},
		{"bad server", func(n *NFSMount) { n.Server = "a b" }, "not a hostname"},
		{"mdns", func(n *NFSMount) { n.Server = "TrueNAS.Local" }, "multicast DNS"},
		{"no export", func(n *NFSMount) { n.Export = "" }, "export path is required"},
		{"relative", func(n *NFSMount) { n.Export = "mnt/data" }, "must be absolute"},
		{"root", func(n *NFSMount) { n.Export = "/" }, "whole tree"},
		{"comma", func(n *NFSMount) { n.Export = "/mnt/a,b" }, "cannot carry"},
		{"option injection", func(n *NFSMount) { n.Options = "rw addr" }, "cannot carry"},
		{"addr override", func(n *NFSMount) { n.Options = "nfsvers=4,addr=10.0.0.1" }, "must not set addr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := nfsVolume("data", ScopeProject)
			c.edit(v.NFS)
			err := s.ValidateVolume(v, "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestNFSExportsMustNotNest(t *testing.T) {
	s := &Settings{Volumes: []Volume{nfsVolume("data", ScopeProject)}}

	inner := nfsVolume("inner", ScopeShared)
	inner.NFS.Export = "/mnt/hdd-4tb/data/media"
	if err := s.ValidateVolume(inner, ""); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("a nested export got through: %v", err)
	}

	elsewhere := nfsVolume("inner", ScopeShared)
	elsewhere.NFS.Server = "192.168.20.176"
	elsewhere.NFS.Export = "/mnt/hdd-4tb/data/media"
	if err := s.ValidateVolume(elsewhere, ""); err != nil {
		t.Fatalf("the same path on another server does not overlap: %v", err)
	}

	// A share inside a share: there is no per-project directory to expose.
	library := nfsVolume("library", ScopeShared)
	library.NFS.Export = "/mnt/hdd-4tb/oein"
	music := nfsVolume("music", ScopeShared)
	music.NFS.Export = "/mnt/hdd-4tb/oein/music"
	shares := &Settings{Volumes: []Volume{library}}
	if err := shares.ValidateVolume(music, ""); err != nil {
		t.Fatalf("nested shared volumes have nothing to isolate: %v", err)
	}
}

// Converting a volume keeps its old host path for rollback, and that path
// must not trip the overlap check against the volume's own new self or
// against later host-path registrations.
func TestConvertedVolumeKeepsValidating(t *testing.T) {
	converted := nfsVolume("data", ScopeProject)
	converted.Path = "/mnt/data-hdd"
	s := &Settings{Volumes: []Volume{converted}}
	if err := s.ValidateVolume(converted, "data"); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateVolume(Volume{Name: "other", Path: "/mnt/data-hdd/x", Scope: ScopeProject}, ""); err != nil {
		t.Fatalf("host paths should not be compared with an NFS volume's old path: %v", err)
	}
}

func TestNFSRelKeepsProjectsApart(t *testing.T) {
	project := nfsVolume("data", ScopeProject)
	shared := nfsVolume("media", ScopeShared)

	cases := []struct {
		v    Volume
		sub  string
		want string
	}{
		{project, "", "abcd1234"},
		{project, "repos", "abcd1234/repos"},
		{project, "../other", "abcd1234/other"},
		{project, "../../etc", "abcd1234/etc"},
		{shared, "", ""},
		{shared, "covers", "covers"},
		{shared, "../..", ""},
	}
	for _, c := range cases {
		if got := c.v.NFSRel("abcd1234", c.sub); got != c.want {
			t.Errorf("NFSRel(%q) on %s = %q, want %q", c.sub, c.v.Scope, got, c.want)
		}
	}
}

func TestNFSDockerVolumeFollowsTheSource(t *testing.T) {
	v := nfsVolume("data", ScopeProject)
	name := v.NFSDockerVolume("abcd1234")
	if !strings.HasPrefix(name, NFSVolumePrefix+"data-") {
		t.Fatalf("name = %q", name)
	}
	if name != v.NFSDockerVolume("abcd1234") {
		t.Fatal("the name must be stable across deploys")
	}
	if name == v.NFSDockerVolume("other") {
		t.Fatal("two directories must not share a docker volume")
	}
	moved := nfsVolume("data", ScopeProject)
	moved.NFS.Server = "192.168.20.176"
	if name == moved.NFSDockerVolume("abcd1234") {
		t.Fatal("docker volumes are immutable, so a new source needs a new name")
	}
}

func TestNFSDriverOptions(t *testing.T) {
	v := nfsVolume("data", ScopeProject)
	opts := v.NFSDriverOptions("abcd1234/repos")
	if opts["type"] != "nfs" {
		t.Errorf("type = %q", opts["type"])
	}
	if opts["device"] != ":/mnt/hdd-4tb/data/abcd1234/repos" {
		t.Errorf("device = %q", opts["device"])
	}
	if opts["o"] != "addr=192.168.20.175,"+NFSDefaultOptions {
		t.Errorf("o = %q", opts["o"])
	}
	v.NFS.Options = "nfsvers=4.1,soft"
	if got := v.NFSDriverOptions("x")["o"]; got != "addr=192.168.20.175,nfsvers=4.1,soft" {
		t.Errorf("custom o = %q", got)
	}
}

// The display form is also what the delete response reports as retained.
func TestDirDescribesTheExport(t *testing.T) {
	v := nfsVolume("data", ScopeProject)
	v.Path = "/mnt/data-hdd"
	if got := v.Dir("abcd1234"); got != "192.168.20.175:/mnt/hdd-4tb/data/abcd1234" {
		t.Fatalf("Dir = %q", got)
	}
}

func TestParseNFSURL(t *testing.T) {
	got, ok := ParseNFSURL("nfs://192.168.20.175/mnt/hdd-4tb/oein/음악")
	if !ok || got.Server != "192.168.20.175" || got.Export != "/mnt/hdd-4tb/oein/음악" {
		t.Fatalf("got %+v, %v", got, ok)
	}
	for _, bad := range []string{"/mnt/data", "nfs://server", "http://x/y"} {
		if _, ok := ParseNFSURL(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}
