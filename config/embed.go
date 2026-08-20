package config

import "embed"

//go:embed settings.json models.json
var embedded embed.FS
