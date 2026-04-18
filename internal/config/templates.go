package config

import _ "embed"

// Starter yaml shipped inside the binary. The source of truth lives at
// internal/config/templates/*.yaml so edits land in git and cover new
// installs; go:embed pulls them in at build time so the deployed binary
// needs no files on disk.
//
// To change what `spout login` or `spout init` writes, edit the file in
// internal/config/templates/ - do NOT edit a string constant here.

//go:embed templates/global.yaml
var GlobalTemplate string

//go:embed templates/project.yaml
var ProjectTemplate string
