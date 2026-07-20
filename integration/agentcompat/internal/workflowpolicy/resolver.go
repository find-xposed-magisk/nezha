package workflowpolicy

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	resolvedRefPattern    = regexp.MustCompile(`^\$\{\{\s*steps\.([A-Za-z0-9_-]+)\.outputs\.sha\s*\}\}$`)
	resolverRemotePattern = regexp.MustCompile(`(?m)^\s*remote=['"]https://github\.com/(nezhahq/(?:agent|nezha))\.git['"]\s*$`)
)

type refResolver struct {
	id         string
	repository Repository
}

func validatedRefResolver(step *yaml.Node) (refResolver, bool) {
	idNode, hasID := mappingValue(step, "id")
	runNode, hasRun := mappingValue(step, "run")
	id, literalID := scalarString(idNode)
	command, literalRun := scalarString(runNode)
	if !hasID || !hasRun || !literalID || !literalRun || strings.TrimSpace(id) == "" {
		return refResolver{}, false
	}
	lines := make([]string, 0, 7)
	for _, line := range strings.Split(command, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) != 7 || lines[0] != "set -euo pipefail" {
		return refResolver{}, false
	}
	remoteMatch := resolverRemotePattern.FindStringSubmatch(lines[1])
	if len(remoteMatch) != 2 {
		return refResolver{}, false
	}
	repository := Repository(remoteMatch[1])
	branch := "main"
	if repository == RepositoryNezha {
		branch = "master"
	}
	expectedLines := []string{
		lines[0],
		lines[1],
		"mapfile -t refs < <(git ls-remote \"$remote\" refs/heads/" + branch + ")",
		"(( ${#refs[@]} == 1 ))",
		"sha=${refs[0]%%$'\\t'*}",
		`[[ "$sha" =~ ^[0-9a-f]{40}$ ]]`,
		`printf 'sha=%s\n' "$sha" >> "$GITHUB_OUTPUT"`,
	}
	for index, expected := range expectedLines {
		if lines[index] != expected {
			return refResolver{}, false
		}
	}
	return refResolver{id: id, repository: repository}, true
}
