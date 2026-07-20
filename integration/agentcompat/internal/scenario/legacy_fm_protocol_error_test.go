//go:build linux

package scenario

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLegacyFMProtocol_ParsersRejectMalformedFrames(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{name: "list magic", call: func() error { _, err := parseLegacyFMList([]byte("bad")); return err }},
		{name: "list truncated path", call: func() error { return parseListFrame([]byte{'N', 'Z', 'F', 'N', 0, 0, 0, 4, 'x'}) }},
		{name: "list invalid entry type", call: func() error { return parseListFrame([]byte{'N', 'Z', 'F', 'N', 0, 0, 0, 1, 'x', 2, 1, 'a'}) }},
		{name: "download short", call: func() error { _, err := parseLegacyFMDownload([]byte("NZTD")); return err }},
		{name: "marker mismatch", call: func() error { return requireLegacyFMMarker([]byte("NERR"), "NZUP") }},
		{name: "NERR list response", call: func() error { _, err := parseLegacyFMList([]byte("NERRdenied")); return err }},
		{name: "NERR download response", call: func() error { _, err := parseLegacyFMDownload([]byte("NERRmissing")); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("malformed frame accepted")
			} else if !errors.Is(err, errLegacyFMInvalidFrame) && !errors.Is(err, errLegacyFMUnexpected) && !errors.Is(err, errLegacyFMRemote) {
				t.Fatalf("unexpected error = %v", err)
			}
		})
	}
}

func TestLegacyFMProtocol_RedactsRemotePayloadAndParsedListData(t *testing.T) {
	sensitivePath := "/workspace/secret-fixture/pat-token"
	sensitiveRemote := "NERRremote-pat-token"
	listFrame := make([]byte, 8, 8+len(sensitivePath)+2)
	copy(listFrame, []byte("NZFN"))
	binary.BigEndian.PutUint32(listFrame[4:], uint32(len(sensitivePath)))
	listFrame = append(listFrame, []byte(sensitivePath)...)
	listFrame = append(listFrame, 0)

	listErr := listErrorForTest(listFrame)
	remoteErr := listErrorForTest([]byte(sensitiveRemote))
	for _, err := range []error{listErr, remoteErr} {
		require.Error(t, err)
		require.NotContains(t, err.Error(), sensitivePath)
		require.NotContains(t, err.Error(), "pat-token")
		require.NotContains(t, err.Error(), "NERRremote")
	}
}

func TestLegacyFMProtocol_RedactsMarkerMismatch(t *testing.T) {
	err := requireLegacyFMMarker([]byte("wrong-secret-marker"), "expected-secret-marker")

	require.ErrorIs(t, err, errLegacyFMUnexpected)
	require.NotContains(t, err.Error(), "wrong-secret-marker")
	require.NotContains(t, err.Error(), "expected-secret-marker")
}

func listErrorForTest(frame []byte) error {
	_, err := parseLegacyFMList(frame)
	return err
}

func parseListFrame(frame []byte) error {
	_, err := parseLegacyFMList(frame)
	return err
}
