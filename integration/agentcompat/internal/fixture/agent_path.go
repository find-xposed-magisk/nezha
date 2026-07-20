package fixture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var agentRootNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type AgentRoot struct {
	absolute string
}

type AgentPath struct {
	absolute string
	relative string
}

func NewAgentRoot(parent, agentID string) (AgentRoot, error) {
	cleanParent := filepath.Clean(parent)
	if !filepath.IsAbs(cleanParent) {
		return AgentRoot{}, errors.New("agent fixture parent must be absolute")
	}
	if !agentRootNamePattern.MatchString(agentID) {
		return AgentRoot{}, errors.New("invalid agent fixture root name")
	}
	parentInfo, err := os.Lstat(cleanParent)
	if err != nil {
		return AgentRoot{}, fmt.Errorf("inspect agent fixture parent: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return AgentRoot{}, errors.New("agent fixture parent must be a real directory")
	}
	absolute := filepath.Join(cleanParent, agentID)
	if err := os.Mkdir(absolute, 0o700); err != nil {
		return AgentRoot{}, fmt.Errorf("create agent fixture root: %w", err)
	}
	return AgentRoot{absolute: absolute}, nil
}

func (root AgentRoot) Absolute() string {
	return root.absolute
}

func (root AgentRoot) Path(relative string) (AgentPath, error) {
	return root.newPath(relative, false)
}

func (root AgentRoot) DestructivePath(relative string) (AgentPath, error) {
	return root.newPath(relative, true)
}

func (root AgentRoot) newPath(relative string, destructive bool) (AgentPath, error) {
	nativeRelative, err := validateRelativeAgentPath(relative, destructive)
	if err != nil {
		return AgentPath{}, err
	}
	absolute := filepath.Clean(filepath.Join(root.absolute, nativeRelative))
	containedRelative, err := filepath.Rel(root.absolute, absolute)
	if err != nil || filepath.IsAbs(containedRelative) || containedRelative == ".." || strings.HasPrefix(containedRelative, ".."+string(filepath.Separator)) {
		return AgentPath{}, rejectPath(PathRejectionEscape)
	}
	if destructive && containedRelative == "." {
		return AgentPath{}, rejectPath(PathRejectionDestructiveRoot)
	}
	if err := ensureRealParentDirectories(root.absolute, nativeRelative); err != nil {
		return AgentPath{}, err
	}
	if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return AgentPath{}, rejectPath(PathRejectionSymlinkFinal)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return AgentPath{}, fmt.Errorf("inspect agent fixture path: %w", err)
	}
	return AgentPath{absolute: absolute, relative: nativeRelative}, nil
}

func (path AgentPath) String() string {
	return path.absolute
}

func (path AgentPath) Relative() string {
	return path.relative
}

func validateRelativeAgentPath(candidate string, destructive bool) (string, error) {
	if strings.TrimSpace(candidate) == "" {
		return "", rejectPath(PathRejectionEmpty)
	}
	if hasWindowsAbsolutePath(candidate) {
		return "", rejectPath(PathRejectionAbsolute)
	}
	if hasWindowsVolume(candidate) {
		return "", rejectPath(PathRejectionVolume)
	}
	if filepath.IsAbs(candidate) {
		return "", rejectPath(PathRejectionAbsolute)
	}
	if strings.Contains(candidate, `\`) {
		return "", rejectPath(PathRejectionSeparator)
	}
	if strings.Contains(candidate, ":") {
		return "", rejectPath(PathRejectionADS)
	}
	components := strings.Split(candidate, "/")
	for _, component := range components {
		if component == "" {
			return "", rejectPath(PathRejectionSeparator)
		}
		if component == ".." {
			return "", rejectPath(PathRejectionParent)
		}
	}
	nativeRelative := filepath.FromSlash(candidate)
	if destructive && filepath.Clean(nativeRelative) == "." {
		return "", rejectPath(PathRejectionDestructiveRoot)
	}
	return nativeRelative, nil
}

func hasWindowsAbsolutePath(candidate string) bool {
	return len(candidate) >= 3 && ((candidate[0] >= 'A' && candidate[0] <= 'Z') || (candidate[0] >= 'a' && candidate[0] <= 'z')) && candidate[1] == ':' && (candidate[2] == '\\' || candidate[2] == '/')
}

func hasWindowsVolume(candidate string) bool {
	if strings.HasPrefix(candidate, `\\`) || strings.HasPrefix(candidate, `//`) {
		return true
	}
	return len(candidate) >= 2 && ((candidate[0] >= 'A' && candidate[0] <= 'Z') || (candidate[0] >= 'a' && candidate[0] <= 'z')) && candidate[1] == ':'
}

func ensureRealParentDirectories(root, relative string) error {
	parent := filepath.Dir(relative)
	if parent == "." {
		return nil
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open agent fixture root: %w", err)
	}
	defer rootHandle.Close()

	current := ""
	for _, component := range strings.Split(filepath.ToSlash(parent), "/") {
		if current == "" {
			current = component
		} else {
			current = filepath.Join(current, component)
		}
		info, statErr := rootHandle.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := rootHandle.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create agent fixture parent: %w", err)
			}
			info, statErr = rootHandle.Lstat(current)
		}
		if statErr != nil {
			return fmt.Errorf("inspect agent fixture parent: %w", statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return rejectPath(PathRejectionSymlinkParent)
		}
	}
	return nil
}
