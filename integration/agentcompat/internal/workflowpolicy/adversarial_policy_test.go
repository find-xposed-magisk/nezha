package workflowpolicy_test

import (
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workflowpolicy"
)

func TestPolicy_RejectsAdversarialExecutionForms(t *testing.T) {
	tests := []struct {
		fixture    string
		rule       workflowpolicy.Rule
		diagnostic string
	}{
		{fixture: "swallowed-semicolon-true.yml", rule: workflowpolicy.RuleSwallowedFailure, diagnostic: "failure"},
		{fixture: "swallowed-semicolon-colon.yml", rule: workflowpolicy.RuleSwallowedFailure, diagnostic: "failure"},
		{fixture: "swallowed-trap-exit.yml", rule: workflowpolicy.RuleSwallowedFailure, diagnostic: "failure"},
		{fixture: "git-config-mutation.yml", rule: workflowpolicy.RuleRepositoryNotLiteral, diagnostic: "Git configuration"},
		{fixture: "git-url-mutation.yml", rule: workflowpolicy.RuleRepositoryNotLiteral, diagnostic: "Git configuration"},
		{fixture: "relative-workspace-executable.yml", rule: workflowpolicy.RuleReusableExecutable, diagnostic: "workspace"},
		{fixture: "container-runtime-alias.yml", rule: workflowpolicy.RuleContainerizedExecution, diagnostic: "container"},
	}
	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			assertFixtureRejected(t, rejected(test.fixture, test.rule, test.diagnostic))
		})
	}
}
