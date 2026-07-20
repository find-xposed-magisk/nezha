//go:build linux

package scenario

import (
	"os"

	"sigs.k8s.io/yaml"
)

func ReadConfigFile(path string) (AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentConfig{}, err
	}
	var config AgentConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return AgentConfig{}, err
	}
	return config, nil
}
