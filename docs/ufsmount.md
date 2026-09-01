# ufsmount

`ufsmount` exposes any `ufs`-supported virtual file system as a regular
directory on the host using FUSE (Linux only). Files inside the mount
point can be accessed by any program — `ls`, `cat`, editors, build tools,
and so on.

## Install

```bash
go install github.com/cloudfra/ufs/cmd/ufsmount@latest
```

## Prerequisites

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

## Usage

```
ufsmount -uri <source> -mount <path>
```

| Flag     | Required | Description                                             |
|----------|----------|---------------------------------------------------------|
| `-uri`   | yes      | URI (or path) of the virtual file system to mount.      |
| `-mount` | yes      | Directory on the host where the FS will appear.         |

The process runs in the foreground. Press `Ctrl-C` or send `SIGTERM` to
unmount and exit. You can also unmount externally:

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

### Mount a GCS bucket (read-only)

```bash
mkdir -p /mnt/bucket
ufsmount -uri gs://my-public-bucket -mount /mnt/bucket
```

In another terminal:

```bash
ls /mnt/bucket
cat /mnt/bucket/data/report.csv
```

GCS mounts are read-only. Attempting to create or modify files will
return a permission error.

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

Archives (`.zip`, `.tar`, `.tar.gz`, `.tar.bz2`, `.7z`) are mounted
read-only.

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

## Supported operations

| Operation        | Read-write FS | Read-only FS |
|------------------|---------------|--------------|
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

## Signals

| Signal            | Behavior                          |
|-------------------|-----------------------------------|
| `SIGINT` (`^C`)   | Unmount and exit cleanly.         |
| `SIGTERM`         | Unmount and exit cleanly.         |
| Context cancel    | Automatic unmount (library use).  |
