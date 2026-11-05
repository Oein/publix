# NFS volumes

publix can mount an NFS export itself. Register the export as a volume and
projects use it like any other, by name, with the same `deployment.yaml`.

## Why not a host mount

A host-path volume trusts the host. If `/mnt/nas` is an NFS mount that failed
at boot, the path still exists: it is the empty directory underneath. Docker
binds that directory without complaint and the application runs, reporting
missing data and writing new data to the local disk. Nothing fails, and the
mount coming back later does not reach containers that already started.

An NFS volume cannot end up there. The Docker daemon does the mount when a
container starts, and if it cannot, the container does not start.

## Registering one

In **Settings → Volumes**, choose **NFS export**. Or:

```bash
publix volumes -add data=nfs://192.168.1.10/mnt/tank/data
publix volumes -add media=nfs://192.168.1.10/mnt/tank/media -shared
```

The dashboard test-mounts the export and refuses it if that fails, so a typo
shows up there rather than on a deploy. The CLI edits settings directly and
does not test.

Use an IP address. A name is resolved while the host boots, and `.local` names
are refused because multicast DNS is not running yet at that point.

Scopes work as they do for a host path: a project-scoped volume gives each
project `<export>/<project id>`, a shared one mounts the export itself.
Exports may not nest, except that one shared volume may sit inside another.

Mount options default to `nfsvers=3,hard,timeo=600,retrans=2,noatime`. The
`-nfs-options` flag and the dashboard field replace them entirely.

## Moving an existing volume

The layout matches, so pointing a volume at the export its host path already
mounts moves no data:

```bash
publix volumes -nfs data=nfs://192.168.1.10/mnt/tank/data
```

Then redeploy each project that uses it with
`POST /api/projects/{id}/redeploy`, which releases the live version again
instead of building what is new on the branch. A running container keeps its
old mount until then, so this can be done one project at a time. The old path is
kept, and `publix volumes -no-nfs data` goes back to it.

Once every project has moved, the host's own mount is no longer used by
publix and its `fstab` line and the `/mnt` bind on the publix container can go.

## What happens when the NAS is away

- **A deploy** checks the server first and fails in seconds, naming it. The
  running deployment is not touched.
- **A running container** blocks on file access and resumes when the NAS
  returns, because mounts are `hard`.
- **A boot** with the NAS still down leaves containers stopped, since Docker
  tries a restart-policy container only once. publix checks every 30 seconds
  and starts them when the server answers.

That last step only applies to containers the daemon failed to mount. One an
operator stopped stays stopped. Containers that use an export start slowly
while the NAS is down, as each mount attempt can take about three minutes to
give up; other containers are not affected, though the daemon's boot may take
longer.

## Requirements

- Docker's `local` driver mounts through the host kernel, so the host needs
  NFS support (`nfs-common` on Debian and Ubuntu).
- The first use pulls `busybox`, which creates project directories on the
  export. It runs with no network and only sees the export.
- The export must let root write unless the volume is read-only: map root to a
  user that owns it, or disable root squash.
