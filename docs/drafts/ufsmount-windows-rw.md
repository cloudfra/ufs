# ufsmount — Windows ProjFS read-write updates

> **Draft:** This file contains proposed documentation changes for
> `docs/ufsmount.md` once the ProjFS read-write implementation lands.
> Copy the relevant sections into `docs/ufsmount.md` when the PR merges.

## Platform table (replaces existing)

| Platform | Backend | Read | Write |
|----------|---------|------|-------|
| Linux    | FUSE    | yes  | yes (if the FS supports it) |
| Windows  | ProjFS  | yes  | yes (write-back; if the FS supports it) |

## Windows (ProjFS) — Supported operations (replaces existing section)

When the backing file system implements the read-write `FS` interface,
ProjFS mounts support write operations via a write-back model: writes
land on the local NTFS directory first, then ProjFS notifications sync
them back to the backing FS when the file handle is closed. When the
backing FS is read-only (`ReadFS`), destructive operations are denied.

| Operation        | Read-write FS | Read-only FS |
|------------------|---------------|--------------|
| Read file        | yes           | yes          |
| Stat / readdir   | yes           | yes          |
| Create file      | yes (write-back) | no (bypasses ProjFS, writes to local NTFS) |
| Write (truncate) | yes (write-back) | no (bypasses ProjFS, writes to local NTFS) |
| Write (append)   | yes (write-back) | no (bypasses ProjFS, writes to local NTFS) |
| mkdir            | yes (write-back) | no (bypasses ProjFS, writes to local NTFS) |
| Remove file      | yes           | denied       |
| Remove directory | yes           | denied       |
| Rename           | yes (write-back) | denied   |
| Symlink / link   | not yet       | not yet      |
| chmod / chown    | not applicable | not applicable |

**Write-back model:** ProjFS cannot intercept writes in-flight. Instead,
writes materialize on the local NTFS directory. When the file handle is
closed, ProjFS notifies the provider, which reads the modified file from
disk and writes it into the backing FS. This means:

- Writes are **not visible** in the backing FS until the file handle
  is closed.
- If the process crashes before sync completes, the backing FS may
  not reflect the latest write. The local NTFS copy is authoritative
  until sync.
- On a read-only mount, new file creation and writes bypass ProjFS
  entirely — they land on local NTFS without being synced anywhere.
  This is a ProjFS limitation that cannot be worked around.

## Windows examples (replaces existing "read-only" examples)

### Windows: Mount a local directory

```powershell
mkdir C:\mnt\data
ufsmount -uri C:\srv\data -mount C:\mnt\data
```

In another terminal:

```powershell
dir C:\mnt\data
type C:\mnt\data\README.md

# Write operations (synced back to C:\srv\data)
echo "hello" > C:\mnt\data\newfile.txt
type C:\mnt\data\newfile.txt    # prints "hello"
del C:\mnt\data\newfile.txt
```

### Windows: Mount an in-memory file system

```powershell
mkdir C:\mnt\scratch
ufsmount -uri memory:// -mount C:\mnt\scratch
```

In another terminal:

```powershell
echo "temporary" > C:\mnt\scratch\note.txt
type C:\mnt\scratch\note.txt    # prints "temporary"
dir C:\mnt\scratch               # note.txt
```

After stopping `ufsmount`, the data is gone (memory backend).

### Windows: Mount an archive (read-only)

Archives are read-only. Write operations are not synced.

```powershell
mkdir C:\mnt\archive
ufsmount -uri C:\tmp\release.tar.gz -mount C:\mnt\archive
```

```powershell
dir C:\mnt\archive
type C:\mnt\archive\README.md
```

### Windows: Mount a GCS bucket (read-only)

```powershell
mkdir C:\mnt\bucket
ufsmount -uri gs://my-public-bucket -mount C:\mnt\bucket
```

```powershell
dir C:\mnt\bucket
type C:\mnt\bucket\data\report.csv
```

### Windows: Mount with nested overlays

```powershell
mkdir C:\mnt\combined
ufsmount -uri "memory:?cache=null%3A" -mount C:\mnt\combined
```

Writes to the memory root are synced; writes to `cache\` are discarded
(null backend).

```powershell
echo "kept" > C:\mnt\combined\data.txt
echo "discarded" > C:\mnt\combined\cache\tmp.txt
type C:\mnt\combined\data.txt          # prints "kept"
type C:\mnt\combined\cache\tmp.txt     # empty (null backend)
```
