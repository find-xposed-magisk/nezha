package workflowpolicy

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var matrixRunnerPattern = regexp.MustCompile(`^\$\{\{\s*matrix\.([A-Za-z0-9_-]+)\s*\}\}$`)
var githubHostedRunnerPattern = regexp.MustCompile(`^(?:ubuntu|windows|macos)(?:-[A-Za-z0-9.]+)?$`)

func (c *checker) checkRunner(jobPath string, job *yaml.Node) {
	runner, exists := mappingValue(job, "runs-on")
	if !exists {
		c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", job), "jobs must declare a literal GitHub-hosted runner")
		return
	}
	if containsScalar(runner, "self-hosted") {
		c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", runner), "self-hosted runners are forbidden")
		return
	}
	if runner.Kind == yaml.SequenceNode {
		if containsExpression(runner) || !allGitHubHostedLabels(runner) {
			c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", runner), "runs-on entries must be literal GitHub-hosted labels")
		}
		return
	}
	runnerValue, literal := scalarString(runner)
	if !literal {
		c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", runner), "runs-on must be a literal GitHub-hosted runner or static matrix axis")
		return
	}
	if !strings.Contains(runnerValue, "${{") {
		if !githubHostedRunnerPattern.MatchString(runnerValue) {
			c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", runner), "runs-on must use a GitHub-hosted runner")
		}
		return
	}
	matrixMatch := matrixRunnerPattern.FindStringSubmatch(runnerValue)
	if len(matrixMatch) != 2 {
		c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", runner), "runs-on must be a literal GitHub-hosted runner or static matrix axis")
		return
	}
	strategy, hasStrategy := mappingValue(job, "strategy")
	matrix, hasMatrix := mappingValue(strategy, "matrix")
	if include, hasInclude := mappingValue(matrix, "include"); hasInclude {
		c.reject(RuleSelfHostedRunner, at(jobPath+".strategy.matrix.include", include), "matrix include is forbidden for runner selection")
		return
	}
	runnerAxis, hasRunnerAxis := mappingValue(matrix, matrixMatch[1])
	if !hasStrategy || !hasMatrix || !hasRunnerAxis || containsExpression(runnerAxis) || !allGitHubHostedLabels(runnerAxis) {
		c.reject(RuleSelfHostedRunner, at(jobPath+".runs-on", runner), "runs-on matrix axis must contain only literal GitHub-hosted labels")
		return
	}
	if containsScalar(runnerAxis, "self-hosted") {
		c.reject(RuleSelfHostedRunner, at(jobPath+".strategy.matrix."+matrixMatch[1], runnerAxis), "self-hosted runners are forbidden")
	}
}

func allGitHubHostedLabels(node *yaml.Node) bool {
	valid := true
	walkScalars(node, func(scalar *yaml.Node) {
		if !githubHostedRunnerPattern.MatchString(scalar.Value) {
			valid = false
		}
	})
	return valid
}

func containsExpression(node *yaml.Node) bool {
	found := false
	walkScalars(node, func(scalar *yaml.Node) {
		if strings.Contains(scalar.Value, "${{") {
			found = true
		}
	})
	return found
}

func (c *checker) checkRequiredCacheDisabled(path string, step *yaml.Node) {
	with, hasWith := mappingValue(step, "with")
	cache, hasCache := mappingValue(with, "cache")
	if hasWith && hasCache && explicitFalse(cache) {
		return
	}
	node := step
	if hasCache {
		node = cache
	}
	c.reject(RuleReusableExecutable, at(path+".with.cache", node), "actions/setup-go requires cache: false")
}
