# ufsmount

`ufsmount` exposes any `ufs`-supported virtual file system through one of two
outputs: as a regular directory on the host, or as the FS service of
`proto/ufs.proto` on a socket that another process — or another machine — can
read and write. `-mount` decides which one it is: a path on the host is a mount,
and a `grpc://host:port` address is the socket to serve on. Files inside the
mount point can be accessed by any program — `ls`, `cat`, editors, build tools,
and so on.

The mount mechanism is platform-specific:

| Platform | Backend | Read | Write                       |
| -------- | ------- | ---- | --------------------------- |
| Linux    | FUSE    | yes  | yes (if the FS supports it) |
| Windows  | ProjFS  | yes  | no (read-only)              |

The socket output needs no platform support of its own: it is the same on
every platform `ufs` builds for.

## Install

```bash
go install github.com/cloudfra/ufs/cmd/ufsmount@latest
```

## Prerequisites

A `-mount` path needs something installed on the host: FUSE on Linux, ProjFS on
Windows. A `-mount` address and `-debug` need a free port and nothing else.

### Linux — FUSE

FUSE must be available on the host:

```bash
# Debian / Ubuntu
sudo apt-get install -y fuse3

# Fedora / RHEL
sudo dnf install -y fuse3

# Verify
ls -l /dev/fuse
```

The user running `ufsmount` must have access to `/dev/fuse` (typically
via the `fuse` group).

### Windows — ProjFS

Windows Projected File System (ProjFS) must be enabled. It is available
on Windows 10 version 1809 and later.

Enable it from an elevated PowerShell prompt:

```powershell
Enable-WindowsOptionalFeature -Online -FeatureName Client-ProjFS
```

A reboot may be required. See the
[Microsoft ProjFS documentation](https://learn.microsoft.com/en-us/windows/win32/projfs/enabling-windows-projected-file-system)
for details.

## Usage

```text
ufsmount -uri <source> -mount <path>|grpc://<host:port> [-debug <host:port>] [-verbose]
```

| Flag       | Required | Description                                                                                                 |
| ---------- | -------- | ----------------------------------------------------------------------------------------------------------- |
| `-uri`     | yes      | URI (or path) of the virtual file system to expose.                                                         |
| `-mount`   | yes      | Directory on the host where the FS will appear, or a `grpc://host:port` address to serve the FS on instead. |
| `-debug`   | no       | `host:port` address to run the debug HTTP server on (expvar and the Go profiler).                           |
| `-verbose` | no       | Log every FUSE/ProjFS callback, for debugging.                                                              |

`-mount` carries a path when it names a directory and a URI when it names a
socket, so one flag says which output the file system is exposed through. A
scheme other than `grpc://` in `-mount` is refused rather than guessed at: `-uri`
is where the file system's own URI goes.

The process runs in the foreground. Press `Ctrl-C` or send `SIGTERM` to
unmount, let any request already in flight finish, and exit. You can also
unmount externally:

```bash
fusermount -u /mnt/data
```

## Examples

### Mount a local directory

Mount one directory at another location. Reads and writes go through
to the source directory.

```bash
mkdir -p /mnt/data
ufsmount -uri /srv/data -mount /mnt/data
```

In another terminal:

```bash
ls /mnt/data
cat /mnt/data/README.md
echo "hello" > /mnt/data/newfile.txt
```

### Mount an in-memory file system

Create a volatile scratch space that disappears when `ufsmount` exits.

```bash
mkdir -p /mnt/scratch
ufsmount -uri memory:// -mount /mnt/scratch
```

In another terminal:

```bash
echo "temporary" > /mnt/scratch/note.txt
cat /mnt/scratch/note.txt    # prints "temporary"
ls /mnt/scratch              # note.txt
```

After stopping `ufsmount`, the data is gone.

### Serve a file system over gRPC

Write an address instead of a path in `-mount` and the file system is not
mounted at all: it answers the FS service of `proto/ufs.proto` on a socket, so
another process — or another machine — reads and writes it over gRPC.

```bash
ufsmount -uri memory:// -mount grpc://0.0.0.0:9000
```

The address is written as a URI because that is how the other end writes it: a
caller reaches this same file system as `grpc://127.0.0.1:9000`, which is the
URI the `grpc://` driver of `drivers/grpcfs` takes. An empty host
(`grpc://:9000`) means every interface. The address carries no path and no
query — the service answers the whole file system, and a subtree is what a
mount point is for.

What is served is plaintext. `grpcs://` is refused rather than answered without
TLS: the scheme is a promise about the transport, and no flag gives this process
a certificate to keep the promise with. Reach a host you do not control over a
network you do.

### Debug endpoints

`-debug` runs an HTTP server beside whichever output was chosen, answering
`/debug/vars` — the `expvar` counters — and `/debug/pprof/*`, the Go profiler.
It says nothing about the file system and reaches none of it.

```bash
ufsmount -uri gs://my-bucket -mount /mnt/bucket -debug 127.0.0.1:6060
```

```bash
curl -s http://127.0.0.1:6060/debug/vars | head
go tool pprof http://127.0.0.1:6060/debug/pprof/heap
```

The profiler exposes the process's memory and goroutines, so bind the address
to somewhere you control — `127.0.0.1:6060` rather than `:6060`.

### Mount a GCS bucket

```bash
mkdir -p /mnt/bucket
ufsmount -uri gs://my-public-bucket -mount /mnt/bucket
```

In another terminal:

```bash
ls /mnt/bucket
cat /mnt/bucket/data/report.csv
```

On Linux the bucket is writable if your credentials allow it: files
created under the mount become objects in the bucket.

### Mount a GCS prefix

```bash
mkdir -p /mnt/logs
ufsmount -uri gs://my-bucket/logs/2026 -mount /mnt/logs
```

Only objects under the `logs/2026` prefix are visible.

### Mount with nested overlays

Use `ufs.CreateURI` query-parameter syntax to layer additional file
systems at specific paths inside the mount.

```bash
mkdir -p /mnt/combined
ufsmount -uri "memory:?cache=null%3A" -mount /mnt/combined
```

This creates a writable memory root with a null (discard) mount at
`cache/`. Writes to `cache/` are silently discarded; writes elsewhere
are stored in memory.

In another terminal:

```bash
echo "kept" > /mnt/combined/data.txt
echo "discarded" > /mnt/combined/cache/tmp.txt
cat /mnt/combined/data.txt         # prints "kept"
cat /mnt/combined/cache/tmp.txt    # empty (null backend)
```

### Mount a local archive

```bash
mkdir -p /mnt/archive
ufsmount -uri /tmp/release.tar.gz -mount /mnt/archive
```

Archives (`.zip`, `.tar`, `.tar.gz`, `.tar.bz2`, `.tar.xz`, `.tar.lz4`,
`.tar.br`, `.tar.zst`, `.rar`, `.7z`) are mounted read-only.

```bash
ls /mnt/archive
cat /mnt/archive/README.md
```

### Mount a remote archive

```bash
mkdir -p /mnt/remote
ufsmount -uri https://example.com/assets.zip -mount /mnt/remote
```

The archive is downloaded to a temporary directory, mounted read-only,
and the temporary files are removed when `ufsmount` exits.

### Mount a null file system

The null backend accepts all writes but discards the data. Reads return
empty content and stat reports everything as a directory.

```bash
mkdir -p /mnt/null
ufsmount -uri null:// -mount /mnt/null
```

In another terminal:

```bash
ls /mnt/null                       # empty
echo "gone" > /mnt/null/file.txt
cat /mnt/null/file.txt             # empty (writes discarded)
```

### Windows: Mount a local directory (read-only)

On Windows, ProjFS projects the virtual file system as read-only.

```powershell
mkdir C:\mnt\data
ufsmount -uri C:\srv\data -mount C:\mnt\data
```

In another terminal:

```powershell
dir C:\mnt\data
type C:\mnt\data\README.md
```

### Windows: Mount an archive

```powershell
mkdir C:\mnt\archive
ufsmount -uri C:\tmp\release.tar.gz -mount C:\mnt\archive
```

```powershell
dir C:\mnt\archive
type C:\mnt\archive\README.md
```

### Windows: Mount a GCS bucket

```powershell
mkdir C:\mnt\bucket
ufsmount -uri gs://my-public-bucket -mount C:\mnt\bucket
```

```powershell
dir C:\mnt\bucket
type C:\mnt\bucket\data\report.csv
```

## Supported operations

### Linux (FUSE)

| Operation        | Read-write FS | Read-only FS |
| ---------------- | ------------- | ------------ |
| Read file        | yes           | yes          |
| Stat / readdir   | yes           | yes          |
| Create file      | yes           | EROFS        |
| Write (truncate) | yes           | EROFS        |
| Write (append)   | not yet       | EROFS        |
| mkdir            | yes           | EROFS        |
| Remove file      | yes           | EROFS        |
| Remove directory | yes           | EROFS        |
| Rename           | not yet       | not yet      |
| Symlink / link   | not yet       | not yet      |
| chmod / chown    | not yet       | not yet      |

### Windows (ProjFS)

ProjFS mounts are always read-only. All file systems — including those
that implement the read-write `FS` interface — are projected as
read-only virtual files.

| Operation      | Any FS                                     |
| -------------- | ------------------------------------------ |
| Read file      | yes                                        |
| Stat / readdir | yes                                        |
| Create file    | no (bypasses ProjFS, writes to local NTFS) |
| Write          | no (bypasses ProjFS, writes to local NTFS) |
| mkdir          | no (bypasses ProjFS, writes to local NTFS) |
| Remove file    | denied                                     |
| Rename         | denied                                     |

ProjFS intercepts delete and rename operations and denies them.
New file creation and writes cannot be intercepted by ProjFS — they
materialize directly to the local NTFS directory rather than being
routed through the virtual file system.

## Signals

| Signal          | Behavior                             |
| --------------- | ------------------------------------ |
| `SIGINT` (`^C`) | Unmount, stop serving, exit cleanly. |
| `SIGTERM`       | Unmount, stop serving, exit cleanly. |
| Context cancel  | Automatic unmount (library use).     |
