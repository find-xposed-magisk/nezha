package workflowpolicy

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

func parseWorkflow(source string, data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, &ParseError{Source: source, Cause: err}
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, &ParseError{Source: source, Cause: errors.New("workflow root must be a mapping")}
	}
	if err := validateYAMLNode(document.Content[0]); err != nil {
		return nil, &ParseError{Source: source, Cause: err}
	}

	var trailing yaml.Node
	err := decoder.Decode(&trailing)
	if err == nil && len(trailing.Content) > 0 {
		return nil, &ParseError{Source: source, Cause: errors.New("multiple YAML documents are not allowed")}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, &ParseError{Source: source, Cause: err}
	}
	return document.Content[0], nil
}

func validateYAMLNode(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("YAML aliases are not allowed at line %d", node.Line)
	}
	if node.Anchor != "" {
		return fmt.Errorf("YAML aliases are not allowed; YAML anchors are not allowed at line %d", node.Line)
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			identity := key.Tag + "\x00" + key.Value
			if _, exists := seen[identity]; exists {
				return fmt.Errorf("duplicate key %q at line %d", key.Value, key.Line)
			}
			seen[identity] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}
