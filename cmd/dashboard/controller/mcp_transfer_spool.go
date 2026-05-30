package controller

import (
	"io"
	"os"
	"runtime"
)

type transferSpool struct {
	f *os.File
}

func newTransferSpool() (*transferSpool, error) {
	f, err := os.CreateTemp("", "nz-mcp-xfer-*")
	if err != nil {
		return nil, err
	}
	// 提前 unlink，文件句柄关掉就回收磁盘；Windows 不支持就留到 Close 兜底。
	if runtime.GOOS != "windows" {
		_ = os.Remove(f.Name())
	}
	return &transferSpool{f: f}, nil
}

func (s *transferSpool) Write(p []byte) (int, error) { return s.f.Write(p) }

func (s *transferSpool) Read(p []byte) (int, error) { return s.f.Read(p) }

func (s *transferSpool) Rewind() error {
	_, err := s.f.Seek(0, io.SeekStart)
	return err
}

func (s *transferSpool) Close() {
	if s.f == nil {
		return
	}
	name := s.f.Name()
	_ = s.f.Close()
	if runtime.GOOS == "windows" {
		_ = os.Remove(name)
	}
	s.f = nil
}
