# walk

`walk` lists every file in a virtual file system. It accepts any URI scheme
supported by `ufs.New` and prints one absolute path per line.

## Install

```bash
go install github.com/cloudfra/ufs/cmd/walk@latest
```

## Usage

```text
walk -path <uri-or-path>
```

| Flag    | Default | Description                                  |
|---------|---------|----------------------------------------------|
| `-path` | `.`     | URI or local path to walk.                   |

## Examples

### Walk a local directory

```bash
walk -path /var/log
```

### Walk the current directory

```bash
walk
```

The default `-path` is `.`, so running `walk` with no arguments lists
every file under the working directory.

### Walk an in-memory file system

```bash
walk -path memory://
```

An empty memory FS has no files, so this produces no output.

### Walk a Google Cloud Storage bucket

```bash
walk -path gs://my-bucket
```

Lists every object in the bucket. Credentials are resolved via
Application Default Credentials; unauthenticated access is tried as a
fallback for public buckets.

### Walk a GCS prefix

```bash
walk -path gs://my-bucket/data/2026
```

Lists only objects under the `data/2026` prefix.

### Walk a local archive

```bash
walk -path /tmp/release.tar.gz
```

Archives (`.zip`, `.tar`, `.tar.gz`, `.tar.bz2`, `.7z`) are mounted
read-only and walked as if they were directories.

### Walk a remote archive

```bash
walk -path https://example.com/assets.zip
```

The archive is downloaded to a temporary directory, mounted read-only,
and the temporary directory is removed when the walk finishes.

### Walk a git repository

```bash
walk -path https://github.com/cloudfra/ufs.git
```

The repository is shallow-cloned into a temporary directory and walked.
