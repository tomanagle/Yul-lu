package main

import (
	"os"

	"github.com/BurntSushi/toml"

	"github.com/tomanagle/yullu/internal/config"
)

// writeConfigTOML marshals cfg back to disk. The default loader supports
// reading TOML; this is the dual for the desktop app's SaveConfig.
func writeConfigTOML(path string, cfg config.Config) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
