//go:build linux

package scenario

import (
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

func ReadConfigFile(path string) (AgentConfig, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return AgentConfig{}, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		return AgentConfig{}, err
	}
	var config AgentConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return AgentConfig{}, err
	}
	return config, nil
}
