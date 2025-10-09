package terraform

import "embed"

//go:embed **
var TerraformFiles embed.FS
