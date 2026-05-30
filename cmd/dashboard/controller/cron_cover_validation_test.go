package controller

import (
	"testing"

	"github.com/nezhahq/nezha/model"
)

// C2 regression: writes must reject unknown Cover values so dirty configs
// cannot be persisted. CronTrigger has no PAT context on the periodic
// scheduler path, so unknown Cover sails past every PAT guard and dispatches
// via the default branch in CronTrigger (no CoverAll/IgnoreAll match → still
// reaches every server that passes cronCanSendToServer).
func TestIsValidCronCover_RejectsUnknown(t *testing.T) {
	cases := []struct {
		name  string
		cover uint8
		want  bool
	}{
		{"CoverIgnoreAll", model.CronCoverIgnoreAll, true},
		{"CoverAll", model.CronCoverAll, true},
		{"CoverAlertTrigger", model.CronCoverAlertTrigger, true},
		{"unknown_99", 99, false},
		{"unknown_max", 255, false},
		{"unknown_3", 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidCronCover(tc.cover); got != tc.want {
				t.Fatalf("isValidCronCover(%d) = %v, want %v", tc.cover, got, tc.want)
			}
		})
	}
}

func TestIsValidServiceCover_RejectsUnknown(t *testing.T) {
	cases := []struct {
		name  string
		cover uint8
		want  bool
	}{
		{"ServiceCoverAll", model.ServiceCoverAll, true},
		{"ServiceCoverIgnoreAll", model.ServiceCoverIgnoreAll, true},
		{"unknown_99", 99, false},
		{"unknown_max", 255, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidServiceCover(tc.cover); got != tc.want {
				t.Fatalf("isValidServiceCover(%d) = %v, want %v", tc.cover, got, tc.want)
			}
		})
	}
}
