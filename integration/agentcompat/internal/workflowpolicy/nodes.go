package workflowpolicy

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for index := 0; index < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true
		}
	}
	return nil, false
}

func mappingEntries(mapping *yaml.Node) [][2]*yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	entries := make([][2]*yaml.Node, 0, len(mapping.Content)/2)
	for index := 0; index < len(mapping.Content); index += 2 {
		entries = append(entries, [2]*yaml.Node{mapping.Content[index], mapping.Content[index+1]})
	}
	return entries
}

func scalarString(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", false
	}
	return node.Value, true
}

func explicitFalse(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(node.Value), "false")
}

func positiveInteger(node *yaml.Node) bool {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return false
	}
	value, err := strconv.Atoi(node.Value)
	return err == nil && value > 0
}

func walkScalars(node *yaml.Node, visit func(*yaml.Node)) {
	if node.Kind == yaml.ScalarNode {
		visit(node)
	}
	for _, child := range node.Content {
		walkScalars(child, visit)
	}
}

func walkMappings(node *yaml.Node, visit func(*yaml.Node)) {
	if node.Kind == yaml.MappingNode {
		visit(node)
	}
	for _, child := range node.Content {
		walkMappings(child, visit)
	}
}

func containsScalar(node *yaml.Node, expected string) bool {
	found := false
	walkScalars(node, func(scalar *yaml.Node) {
		if scalar.Value == expected {
			found = true
		}
	})
	return found
}
