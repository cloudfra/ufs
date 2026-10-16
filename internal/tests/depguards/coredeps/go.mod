module github.com/cloudfra/ufs/internal/tests/depguards/coredeps

go 1.27.1

require github.com/cloudfra/ufs v0.16.1

// Resolve the ufs dependency from this repository's working tree.
replace github.com/cloudfra/ufs => ../../../../

require (
	github.com/STARRY-S/zip v0.2.3 // indirect
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/sevenzip v1.6.5 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/dsnet/compress v0.0.2-0.20230904184137-39efe44ab707 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/klauspost/pgzip v1.2.7 // indirect
	github.com/mholt/archives v0.1.5 // indirect
	github.com/mikelolasagasti/xz v1.0.1 // indirect
	github.com/minio/minlz v1.2.2 // indirect
	github.com/nwaples/rardecode/v2 v2.4.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/sorairolake/lzip-go v0.3.8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/stangelandcl/ppmd v0.1.1 // indirect
	github.com/ulikunitz/xz v0.5.17 // indirect
	go4.org v0.0.0-20260112195520-a5071408f32f // indirect
	golang.org/x/text v0.43.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
