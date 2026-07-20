package workflowpolicy_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/workflowpolicy"
	"github.com/stretchr/testify/require"
)

func TestPolicy_RejectsMissingRequiredDependency(t *testing.T) {
	// Given
	data, err := os.ReadFile(fixturePath(t, "missing-required-dependency.yml"))
	require.NoError(t, err)

	// When
	err = workflowpolicy.Verify(data, workflowpolicy.RepositoryNezha)

	// Then
	var policyError *workflowpolicy.PolicyError
	require.ErrorAs(t, err, &policyError)
	require.True(t, policyError.Has(workflowpolicy.RuleWorkflowStructure))
}

func TestPolicy_RejectsInvalidRequiredAggregator(t *testing.T) {
	tests := []struct {
		name        string
		currentText string
		invalidText string
	}{
		{
			name:        "missing tests dependency",
			currentText: "needs:\n      - tests\n      - linux-race-quality\n      - agentcompat-stress",
			invalidText: "needs:\n      - linux-race-quality\n      - agentcompat-stress",
		},
		{
			name:        "extra dependency",
			currentText: "      - agentcompat-stress\n    runs-on:",
			invalidText: "      - agentcompat-stress\n      - unrelated-job\n    runs-on:",
		},
		{
			name:        "missing stress dependency",
			currentText: "      - linux-race-quality\n      - agentcompat-stress",
			invalidText: "      - linux-race-quality",
		},
		{
			name:        "missing always condition",
			currentText: "if: ${{ always() }}",
			invalidText: "if: ${{ success() }}",
		},
		{
			name:        "tests result only mentioned",
			currentText: "test \"${{ needs.tests.result }}\" = success",
			invalidText: "printf '%s success\\n' \"${{ needs.tests.result }}\"",
		},
		{
			name:        "quality result only mentioned",
			currentText: "test \"${{ needs.linux-race-quality.result }}\" = success",
			invalidText: "printf '%s success\\n' \"${{ needs.linux-race-quality.result }}\"",
		},
		{
			name:        "stress result only mentioned",
			currentText: "test \"${{ needs.agentcompat-stress.result }}\" = success",
			invalidText: "printf '%s success\\n' \"${{ needs.agentcompat-stress.result }}\"",
		},
		{
			name: "success checks defined but not executed",
			currentText: strings.Join([]string{
				"test \"${{ needs.tests.result }}\" = success",
				"test \"${{ needs.linux-race-quality.result }}\" = success",
				"test \"${{ needs.agentcompat-stress.result }}\" = success",
			}, "\n          "),
			invalidText: strings.Join([]string{
				"check_results() {",
				"  test \"${{ needs.tests.result }}\" = success",
				"  test \"${{ needs.linux-race-quality.result }}\" = success",
				"  test \"${{ needs.agentcompat-stress.result }}\" = success",
				"}",
			}, "\n          "),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			data := readNezhaQualityWorkflow(t)
			invalidWorkflow := strings.Replace(string(data), test.currentText, test.invalidText, 1)
			require.NotEqual(t, string(data), invalidWorkflow, "workflow mutation must match current content")

			// When
			err := workflowpolicy.Verify([]byte(invalidWorkflow), workflowpolicy.RepositoryNezha)

			// Then
			var policyError *workflowpolicy.PolicyError
			require.ErrorAs(t, err, &policyError)
			require.True(t, policyError.Has(workflowpolicy.RuleWorkflowStructure))
		})
	}
}

func TestPolicy_RejectsMissingRequiredAggregator(t *testing.T) {
	assertFixtureRejected(t, rejected("missing-required-aggregator.yml", workflowpolicy.RuleWorkflowStructure, "aggregator is missing"))
}
