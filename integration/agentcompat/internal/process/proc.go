//go:build linux

package process

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

func readRSSBytes(pid int) (uint64, error) {
	path := filepath.Join(strconv.Itoa(pid), "status")
	procRoot, err := os.OpenRoot("/proc")
	if err != nil {
		return 0, err
	}
	defer procRoot.Close()
	file, err := procRoot.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
			kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse VmRSS: %w", err)
			}
			return kilobytes * 1024, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return 0, errors.New("VmRSS not found")
}

func descendantPIDs(rootPID int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	children := make(map[int][]int)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		parentPID, err := readParentPID(pid)
		if err != nil {
			// /proc is a live snapshot: an unrelated process can disappear
			// between ReadDir and reading stat. Root PID reads stay strict.
			if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) {
				continue
			}
			return nil, err
		}
		children[parentPID] = append(children[parentPID], pid)
	}
	descendants := make([]int, 0)
	queue := append([]int(nil), children[rootPID]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		descendants = append(descendants, pid)
		queue = append(queue, children[pid]...)
	}
	sort.Ints(descendants)
	return descendants, nil
}

func readParentPID(pid int) (int, error) {
	path := filepath.Join(strconv.Itoa(pid), "stat")
	procRoot, err := os.OpenRoot("/proc")
	if err != nil {
		return 0, err
	}
	defer procRoot.Close()
	data, err := procRoot.ReadFile(path)
	if err != nil {
		return 0, err
	}
	closingParenthesis := strings.LastIndexByte(string(data), ')')
	if closingParenthesis < 0 {
		return 0, fmt.Errorf("parse %s: missing command terminator", path)
	}
	fields := strings.Fields(string(data[closingParenthesis+1:]))
	if len(fields) < 2 {
		return 0, fmt.Errorf("parse %s: missing parent PID", path)
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("parse %s parent PID: %w", path, err)
	}
	return parentPID, nil
}

type FDObservation struct {
	Number int
	Target string
}

func processFDs(pid int, captureObservations bool) (int, map[uint64]struct{}, []FDObservation, error) {
	directory := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, nil, nil, err
	}
	count := 0
	sockets := make(map[uint64]struct{})
	var observations []FDObservation
	if captureObservations {
		observations = make([]FDObservation, 0, len(entries))
	}
	for _, entry := range entries {
		descriptor, err := strconv.Atoi(entry.Name())
		if err != nil || descriptor < 3 {
			continue
		}
		target, err := os.Readlink(filepath.Join(directory, entry.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, nil, nil, err
		}
		count++
		if captureObservations {
			observations = append(observations, FDObservation{Number: descriptor, Target: target})
		}
		if inode, exists := parseSocketInode(target); exists {
			sockets[inode] = struct{}{}
		}
	}
	if captureObservations {
		sort.Slice(observations, func(left, right int) bool {
			return observations[left].Number < observations[right].Number || (observations[left].Number == observations[right].Number && observations[left].Target < observations[right].Target)
		})
	}
	return count, sockets, observations, nil
}

func parseSocketInode(target string) (uint64, bool) {
	if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
		return 0, false
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
	return inode, err == nil
}

func listeningSocketInodes(pid int, protocol string) (map[uint64]struct{}, error) {
	if protocol != "tcp" && protocol != "tcp6" {
		return nil, errors.New("unsupported proc network protocol")
	}
	path := filepath.Join(strconv.Itoa(pid), "net", protocol)
	procRoot, err := os.OpenRoot("/proc")
	if err != nil {
		return nil, err
	}
	defer procRoot.Close()
	file, err := procRoot.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	listeners := make(map[uint64]struct{})
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		// Skip the stable kernel table header.
	}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s listener inode: %w", path, err)
		}
		listeners[inode] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return listeners, nil
}
