package evidence

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_Redaction(t *testing.T) {
	input := `"Authorization":"Basic authorization-secret" JWT=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature PAT=pat-secret agent_secret_key=agent-secret client_secret=config-secret https://local/mcp/upload/path-secret?token=query-secret`
	redacted := Redact(input)
	for _, secret := range []string{"authorization-secret", "eyJhbGciOiJIUzI1NiJ9", "pat-secret", "agent-secret", "config-secret", "path-secret", "query-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret survived redaction: %q", secret)
		}
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", redacted)
	}
	junit, err := JUnit(Results{Profile: "pr-full", Passed: false, Scenarios: []ScenarioResult{{Name: "secret", Passed: false, Assertions: []Assertion{{Name: "secret failure", Passed: false}}, Error: input}}})
	if err != nil {
		t.Fatalf("marshal redacted junit: %v", err)
	}
	if strings.Contains(string(junit), "authorization-secret") || strings.Contains(string(junit), "path-secret") {
		t.Fatalf("secret survived JUnit redaction: %s", junit)
	}
}

func TestEvidence_Golden(t *testing.T) {
	profile, err := contract.ProfileByName("pr-full")
	if err != nil {
		t.Fatalf("construct profile: %v", err)
	}
	root := t.TempDir()
	nezhaSource := filepath.Join(root, "nezha-source")
	agentSource := filepath.Join(root, "agent-source")
	resultsDir := filepath.Join(root, "results")
	paths, err := contract.NewPaths(nezhaSource, agentSource, resultsDir)
	if err != nil {
		t.Fatalf("construct paths: %v", err)
	}
	metadata, err := NewMetadata(MetadataInput{
		Profile:        profile,
		Seed:           contract.DefaultSeed,
		Paths:          paths,
		ResourceBudget: contract.DefaultResourceBudget(),
		StartedAt:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		EvidenceFiles:  EvidenceFiles(),
	})
	if err != nil {
		t.Fatalf("construct metadata: %v", err)
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	wantJSON := fmt.Sprintf(`{"agent_source":%q,"evidence_files":["metadata.json","results.json","junit.xml","dashboard.log","agents/*.log","transfer.json","reconnect.json","stress.json","cleanup.json","step-summary.md"],"load_classification":"regression loads, not capacity claims","nezha_source":%q,"profile":{"name":"pr-full","job_timeout_seconds":4500,"suite_deadline_seconds":3300,"default_seed":"0x4e5a4841","agent_count":8,"stress_rounds":4,"concurrent_operations":64,"concurrent_sessions_per_kind":4,"transfer_pairs":1,"transfer_bytes":104857600,"dashboard_restart_cycles":1,"iterations":1,"stream_boundary_allowed":40,"stream_boundary_rejected":41},"resource_budget":{"warmup_runs_per_path":1,"baseline_sample_count":5,"end_sample_count":5,"sample_interval_milliseconds":250,"child_process_count_drift":0,"listener_count_drift":0,"non_stdio_fd_count_drift":0,"dashboard_rss_delta_bytes":67108864,"agent_rss_delta_bytes":33554432,"transfer_heap_bytes":16777216},"results_dir":%q,"scenarios":[],"seed":"0x4e5a4841","started_at":"2026-01-02T03:04:05Z"}`, agentSource, nezhaSource, resultsDir)
	if string(data) != wantJSON {
		t.Fatalf("metadata golden mismatch\nwant: %s\ngot:  %s", wantJSON, data)
	}

	results := Results{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: "metadata", Passed: true, Assertions: []Assertion{{Name: "metadata written", Passed: true}}}}}
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal results: %v", err)
	}
	const wantResultsJSON = `{"profile":"pr-full","passed":true,"scenarios":[{"name":"metadata","passed":true,"assertions":[{"name":"metadata written","passed":true}]}]}`
	if string(resultsJSON) != wantResultsJSON {
		t.Fatalf("results golden mismatch\nwant: %s\ngot:  %s", wantResultsJSON, resultsJSON)
	}
	junitXML, err := JUnit(results)
	if err != nil {
		t.Fatalf("marshal junit: %v", err)
	}
	const wantXML = `<testsuite name="pr-full" tests="1" failures="0"><testcase name="metadata"></testcase></testsuite>`
	if string(junitXML) != wantXML {
		t.Fatalf("junit golden mismatch\nwant: %s\ngot:  %s", wantXML, junitXML)
	}
	var parsedJUnit junitSuite
	if err := xml.Unmarshal(junitXML, &parsedJUnit); err != nil {
		t.Fatalf("parse junit golden: %v", err)
	}
	if parsedJUnit.Tests != 1 || parsedJUnit.Failures != 0 || len(parsedJUnit.Cases) != 1 {
		t.Fatalf("unexpected parsed junit: %#v", parsedJUnit)
	}
}

func TestEvidence_SchemaValidation(t *testing.T) {
	invalid := Metadata{}
	if err := invalid.Validate(); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	invalidBudget := ResourceBudgetMetadata{WarmupRunsPerPath: 1, BaselineSampleCount: 5, EndSampleCount: 5, SampleIntervalMilliseconds: 250, DashboardRSSDeltaBytes: 1, AgentRSSDeltaBytes: 1}
	if err := invalidBudget.Validate(); err == nil {
		t.Fatal("missing transfer heap threshold accepted")
	}
	results := Results{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: "failed", Passed: false, Assertions: []Assertion{{Name: "failed assertion", Passed: false}}, Error: "failure"}}}
	if err := results.Validate(); err == nil {
		t.Fatal("inconsistent results accepted")
	}
	for _, result := range []Results{
		{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: "../escape", Passed: true}}},
		{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: "duplicate", Passed: true}, {Name: "duplicate", Passed: true}}},
	} {
		if err := result.Validate(); err == nil {
			t.Fatalf("invalid scenario results accepted: %#v", result)
		}
	}
}

func TestEvidence_ResultsRejectsSuccessfulScenarioWithoutAssertions(t *testing.T) {
	results := Results{Profile: "pr-full", Passed: true, Scenarios: []ScenarioResult{{Name: "registration-config-exec", Passed: true}}}

	if err := results.Validate(); err == nil {
		t.Fatal("successful executable scenario without assertions accepted")
	}
}

func TestEvidence_ResultsJSONRedactsSecrets(t *testing.T) {
	results := Results{Profile: "pr-full", Passed: false, Scenarios: []ScenarioResult{
		{Name: "authorization", Passed: false, Assertions: []Assertion{{Name: "authorization failure", Passed: false}}, Error: "Authorization: Bearer jwt-secret"},
		{Name: "credential-field", Passed: false, Assertions: []Assertion{{Name: "credential failure", Passed: false}}, Error: "agent_secret=agent-secret"},
		{Name: "transfer-token", Passed: false, Assertions: []Assertion{{Name: "transfer failure", Passed: false}}, Error: "/mcp/download/path-token?token=query-token"},
		{Name: "access-token", Passed: false, Assertions: []Assertion{{Name: "access failure", Passed: false}}, Error: "https://local/file?access_token=access-secret"},
		{Name: "api-key", Passed: false, Assertions: []Assertion{{Name: "api failure", Passed: false}}, Error: "https://local/file?api_key=api-secret"},
		{Name: "signature", Passed: false, Assertions: []Assertion{{Name: "signature failure", Passed: false}}, Error: "https://local/file?sig=sig-secret&signature=signature-secret&X-Amz-Signature=aws-secret"},
	}}
	data, err := MarshalResults(results)
	if err != nil {
		t.Fatalf("marshal results: %v", err)
	}
	for _, secret := range []string{"jwt-secret", "agent-secret", "path-token", "query-token", "access-secret", "api-secret", "sig-secret", "signature-secret", "aws-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret survived results JSON: %q in %s", secret, data)
		}
	}
	directData, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("direct marshal results: %v", err)
	}
	if strings.Contains(string(directData), "jwt-secret") || strings.Contains(string(directData), "path-token") {
		t.Fatalf("direct JSON marshal bypassed redaction: %s", directData)
	}
}

func TestEvidence_RedactsXMLTextAttributesAndMalformedFragments(t *testing.T) {
	input := `<record Authorization="Bearer authorization-secret"><agent_secret_key>agent-secret</agent_secret_key><transfer_token>transfer-secret</transfer_token><password>password-secret</password><credential>credential-secret</credential></record>`
	redacted := Redact(input)
	for _, secret := range []string{"authorization-secret", "agent-secret", "transfer-secret", "password-secret", "credential-secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("XML secret survived redaction: %q in %s", secret, redacted)
		}
	}
	var parsed struct {
		XMLName xml.Name `xml:"record"`
		Secret  string   `xml:"agent_secret_key"`
	}
	if err := xml.Unmarshal([]byte(redacted), &parsed); err != nil {
		t.Fatalf("redacted XML is not parseable: %v; output=%s", err, redacted)
	}
	if parsed.Secret != "[REDACTED]" {
		t.Fatalf("unexpected redacted element text: %q", parsed.Secret)
	}
	results := Results{Profile: "pr-full", Passed: false, Scenarios: []ScenarioResult{{Name: "xml", Passed: false, Assertions: []Assertion{{Name: "xml failure", Passed: false}}, Error: input}}}
	junit, err := JUnit(results)
	if err != nil {
		t.Fatalf("marshal XML evidence: %v", err)
	}
	var parsedSuite junitSuite
	if err := xml.Unmarshal(junit, &parsedSuite); err != nil {
		t.Fatalf("redacted JUnit is not parseable: %v; output=%s", err, junit)
	}
	if strings.Contains(string(junit), "agent-secret") || strings.Contains(string(junit), "credential-secret") {
		t.Fatalf("XML evidence secret survived: %s", junit)
	}

	malformed := `<credential>config-secret`
	malformedRedacted := Redact(malformed)
	if strings.Contains(malformedRedacted, "config-secret") {
		t.Fatalf("malformed XML secret survived redaction: %s", malformedRedacted)
	}
}

func TestEvidence_RedactsXMLSensitiveAttributesWithWhitespaceAndMalformedTags(t *testing.T) {
	input := `<record><agent_secret_key value="agent secret with spaces"><credential value=config-secret></credential><password data="password secret with spaces">password text</password></record>`
	redacted := Redact(input)
	for _, secret := range []string{"agent secret with spaces", "config-secret", "password secret with spaces", "password text"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("XML attribute or text secret survived redaction: %q in %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `<agent_secret_key value="[REDACTED]">`) {
		t.Fatalf("sensitive XML attribute was not structurally redacted: %s", redacted)
	}
}

func TestEvidence_RedactsCredentialAttributesWithSpacesAndTruncatedXML(t *testing.T) {
	input := `<record credential="credential secret with spaces"><credential value="truncated secret`
	redacted := Redact(input)
	for _, secret := range []string{"credential secret with spaces", "truncated secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("credential secret survived redaction: %q in %s", secret, redacted)
		}
	}
}

func TestEvidence_RedactsSelfClosingSensitiveXMLWithoutBreakingDocument(t *testing.T) {
	input := `<record><credential value="self-closing secret"/><result>ok</result></record>`
	redacted := Redact(input)
	if strings.Contains(redacted, "self-closing secret") {
		t.Fatalf("self-closing XML secret survived redaction: %s", redacted)
	}
	var parsed struct {
		XMLName xml.Name `xml:"record"`
		Result  string   `xml:"result"`
	}
	if err := xml.Unmarshal([]byte(redacted), &parsed); err != nil {
		t.Fatalf("redacted self-closing XML is not parseable: %v; output=%s", err, redacted)
	}
	if parsed.Result != "ok" {
		t.Fatalf("redacted self-closing XML lost sibling content: %#v", parsed)
	}
}
