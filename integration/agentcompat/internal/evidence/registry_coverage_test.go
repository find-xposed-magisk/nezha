package evidence

import (
	"slices"
	"testing"

	"github.com/nezhahq/nezha/integration/agentcompat/internal/contract"
)

func TestEvidence_RegistryDefinitionsDriveManifestAndProfiles(t *testing.T) {
	manifest := EvidenceFiles()
	for _, definition := range contract.ScenarioDefinitions() {
		dedicatedName := definition.DedicatedArtifactName()
		if dedicatedName != "" && !slices.Contains(manifest, dedicatedName) {
			t.Fatalf("dedicated artifact %q absent from manifest", dedicatedName)
		}
		metadata := validMetadata(t, t.TempDir(), definition.Name)
		profile, err := currentProfile(metadata)
		if err != nil {
			t.Fatalf("profile for %q: %v", definition.Name, err)
		}
		if profile.DedicatedFile != dedicatedName {
			t.Fatalf("scenario %q dedicated file=%q want=%q", definition.Name, profile.DedicatedFile, dedicatedName)
		}
		if definition.Execution == contract.ScenarioExecutionMetadata && profile.Executable {
			t.Fatal("metadata profile unexpectedly executable")
		}
		if definition.Execution != contract.ScenarioExecutionMetadata && !profile.Executable {
			t.Fatalf("scenario %q profile is not executable", definition.Name)
		}
	}
}
