package templates

import _ "embed"

//go:embed Dockerfile
var Dockerfile string

//go:embed docker-compose.yml
var DockerCompose string

//go:embed config.toml
var Config string

//go:embed entrypoint.sh
var Entrypoint string

//go:embed update-all.sh
var UpdateAll string

//go:embed network-filter.sh
var NetworkFilter string
