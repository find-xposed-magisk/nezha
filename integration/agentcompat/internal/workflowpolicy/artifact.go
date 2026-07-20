package workflowpolicy

import (
	"strings"

	"gopkg.in/yaml.v3"
)

const redactedArtifactPath = "${{ runner.temp }}/nezha-agentcompat-redacted"
const redactionCommand = `go run ./integration/agentcompat/cmd/redact --output "$RUNNER_TEMP/nezha-agentcompat-redacted"`

func (c *checker) isRedactionStep(step *yaml.Node) bool {
	run, hasRun := mappingValue(step, "run")
	if !hasRun || run.Kind != yaml.ScalarNode {
		return false
	}
	if strings.TrimSpace(run.Value) != redactionCommand {
		return false
	}
	condition, hasCondition := mappingValue(step, "if")
	if !hasCondition || strings.TrimSpace(condition.Value) != "always()" {
		return false
	}
	for _, key := range []string{"name", "id"} {
		value, exists := mappingValue(step, key)
		if exists && strings.Contains(strings.ToLower(value.Value), "redact") {
			return true
		}
	}
	return false
}

func (c *checker) checkArtifactUpload(path string, step *yaml.Node, redactionComplete bool) {
	if !redactionComplete {
		c.reject(RuleArtifactRedaction, at(path+".uses", step), "artifact upload must immediately follow a redaction step with if: always()")
	}
	condition, hasCondition := mappingValue(step, "if")
	if !hasCondition || strings.TrimSpace(condition.Value) != "always()" {
		c.reject(RuleArtifactRedaction, at(path+".if", step), "artifact upload requires if: always()")
	}
	with, exists := mappingValue(step, "with")
	artifactPath, hasPath := mappingValue(with, "path")
	if !exists || !hasPath || !redactedArtifactPaths(artifactPath.Value) {
		node := step
		if hasPath {
			node = artifactPath
		}
		c.reject(RuleArtifactRedaction, at(path+".with.path", node), "artifact path must reference redacted output")
	}
}

func redactedArtifactPaths(raw string) bool {
	return strings.TrimSpace(raw) == redactedArtifactPath
}
