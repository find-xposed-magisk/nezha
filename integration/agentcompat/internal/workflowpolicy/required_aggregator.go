package workflowpolicy

import (
	"strings"

	"gopkg.in/yaml.v3"
)

func (c *checker) checkRequiredAggregator(jobs *yaml.Node) {
	aggregatorName := map[Repository]string{
		RepositoryAgent: "agent-quality-required", RepositoryNezha: "nezha-quality-required",
	}[c.repository]
	if aggregatorName == "" {
		return
	}
	_, hasTestsJob := mappingValue(jobs, "tests")
	_, hasQualityJob := mappingValue(jobs, "linux-race-quality")
	if !hasTestsJob || !hasQualityJob {
		return
	}
	requiredJobs := []string{"tests", "linux-race-quality", "agentcompat-stress"}
	aggregator, hasAggregator := mappingValue(jobs, aggregatorName)
	if !hasAggregator || aggregator.Kind != yaml.MappingNode {
		c.reject(RuleWorkflowStructure, at("$.jobs."+aggregatorName, jobs), "required quality aggregator is missing")
		return
	}
	needs, hasNeeds := mappingValue(aggregator, "needs")
	if !hasNeeds || !hasExactRequiredNeeds(needs, requiredJobs) {
		c.reject(RuleWorkflowStructure, at("$.jobs."+aggregatorName+".needs", aggregator), "required quality aggregator needs must include every blocking job exactly once")
	}
	condition, hasCondition := mappingValue(aggregator, "if")
	if !hasCondition || !isAlwaysCondition(condition) {
		c.reject(RuleWorkflowStructure, at("$.jobs."+aggregatorName+".if", aggregator), "required quality aggregator must use if: always()")
	}
	steps, hasSteps := mappingValue(aggregator, "steps")
	if !hasSteps || !hasRequiredSuccessChecks(steps, requiredJobs) {
		c.reject(RuleWorkflowStructure, at("$.jobs."+aggregatorName+".steps", aggregator), "required quality aggregator must test every blocking job result for success")
	}
}

func hasExactRequiredNeeds(node *yaml.Node, requiredJobs []string) bool {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) != len(requiredJobs) {
		return false
	}
	required := make(map[string]struct{}, len(requiredJobs))
	for _, jobName := range requiredJobs {
		required[jobName] = struct{}{}
	}
	for _, valueNode := range node.Content {
		value, literal := scalarString(valueNode)
		if !literal {
			return false
		}
		delete(required, value)
	}
	return len(required) == 0
}

func isAlwaysCondition(node *yaml.Node) bool {
	condition, literal := scalarString(node)
	condition = strings.TrimSpace(condition)
	return literal && (condition == "always()" || condition == "${{ always() }}")
}

func hasRequiredSuccessChecks(steps *yaml.Node, requiredJobs []string) bool {
	if steps == nil || steps.Kind != yaml.SequenceNode || len(steps.Content) != 1 {
		return false
	}
	run, hasRun := mappingValue(steps.Content[0], "run")
	command, literal := scalarString(run)
	if !hasRun || !literal {
		return false
	}
	lines := strings.Split(strings.TrimSpace(command), "\n")
	if len(lines) != len(requiredJobs) {
		return false
	}
	requiredChecks := make(map[string]struct{}, len(requiredJobs))
	for _, jobName := range requiredJobs {
		requiredChecks[`test "${{ needs.`+jobName+`.result }}" = success`] = struct{}{}
	}
	for _, line := range lines {
		delete(requiredChecks, strings.TrimSpace(line))
	}
	return len(requiredChecks) == 0
}
