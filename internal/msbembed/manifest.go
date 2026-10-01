package msbembed

// assetChecksums carries the sha256 of the embedded assets for this
// platform. scripts/embed-msb-runtime.sh fills it via the generated
// manifest_checksums.go (gitignored) from the upstream release's
// checksums.sha256. Dev builds without the script keep the empty map:
// the stub variant never consults it, and tagged builds extract
// fail-closed when a checksum is missing.
var assetChecksums = map[string]string{}
