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
	workflowData := readNezhaQualityWorkflow(t)
	workflowVariants := []struct {
		name string
		data []byte
	}{
		{name: "LF", data: workflowData},
		{name: "CRLF", data: []byte(workflowWithCRLF(string(workflowData)))},
	}
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
	for _, variant := range workflowVariants {
		t.Run(variant.name, func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					// Given
					invalidWorkflow := mutateWorkflow(t, variant.data, test.currentText, test.invalidText)

					// When
					err := workflowpolicy.Verify([]byte(invalidWorkflow), workflowpolicy.RepositoryNezha)

					// Then
					var policyError *workflowpolicy.PolicyError
					require.ErrorAs(t, err, &policyError)
					require.True(t, policyError.Has(workflowpolicy.RuleWorkflowStructure))
				})
			}
		})
	}
}

func TestWorkflowWithCRLF_PreservesExistingCRLF(t *testing.T) {
	// Given
	workflow := "first\r\nsecond\r\n"

	// When
	converted := workflowWithCRLF(workflow)

	// Then
	require.Equal(t, workflow, converted)
}

func workflowWithCRLF(workflow string) string {
	// Normalize first so Windows checkouts are not expanded from CRLF to CRCRLF.
	workflow = strings.ReplaceAll(workflow, "\r\n", "\n")
	return strings.ReplaceAll(workflow, "\n", "\r\n")
}

func mutateWorkflow(t *testing.T, data []byte, currentText, invalidText string) string {
	t.Helper()
	workflow := string(data)
	lineEnding := "\n"
	if strings.Contains(workflow, "\r\n") {
		// Windows checkouts preserve CRLF, so multiline mutation snippets must use the source line ending.
		lineEnding = "\r\n"
	}
	currentText = strings.ReplaceAll(currentText, "\n", lineEnding)
	invalidText = strings.ReplaceAll(invalidText, "\n", lineEnding)
	require.Equal(t, 1, strings.Count(workflow, currentText), "workflow mutation must match current content exactly once")
	return strings.Replace(workflow, currentText, invalidText, 1)
}

func TestPolicy_RejectsMissingRequiredAggregator(t *testing.T) {
	assertFixtureRejected(t, rejected("missing-required-aggregator.yml", workflowpolicy.RuleWorkflowStructure, "aggregator is missing"))
}
