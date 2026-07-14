package fixture

import "errors"

var (
	ErrPayloadOverrun      = errors.New("fixture payload exceeds transfer limit")
	ErrPayloadSizeMismatch = errors.New("fixture payload size mismatch")
)

type PathRejectionReason string

const (
	PathRejectionEmpty           PathRejectionReason = "empty"
	PathRejectionAbsolute        PathRejectionReason = "absolute"
	PathRejectionParent          PathRejectionReason = "parent"
	PathRejectionVolume          PathRejectionReason = "volume"
	PathRejectionSeparator       PathRejectionReason = "separator"
	PathRejectionDestructiveRoot PathRejectionReason = "destructive_root"
	PathRejectionEscape          PathRejectionReason = "escape"
	PathRejectionSymlinkParent   PathRejectionReason = "symlink_parent"
	PathRejectionSymlinkFinal    PathRejectionReason = "symlink_final"
	PathRejectionADS             PathRejectionReason = "ads"
)

type AgentPathError struct {
	Reason PathRejectionReason
}

func (e *AgentPathError) Error() string {
	return "agent path rejected: " + string(e.Reason)
}

func rejectPath(reason PathRejectionReason) error {
	return &AgentPathError{Reason: reason}
}
