package config

import "embed"

// embedded is the embedded defaults: the 0.2.0 values moved out of
// code (5, 8) — settings.json (the flag/env defaults) and models.json
// (the models.Defaults rows). The user files merge over them; the
// embedded files are the chain's bottom layer.
//
//go:embed settings.json models.json
var embedded embed.FS
