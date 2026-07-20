package utils

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
)

var _ io.ReadWriteCloser = (*RequestWrapper)(nil)

type RequestWrapper struct {
	req    *http.Request
	reader *bytes.Buffer
	writer net.Conn

	closeOnce sync.Once
	closeInit sync.Once
	closeDone chan struct{}
	closeErr  error
}

func NewRequestWrapper(req *http.Request, writer http.ResponseWriter) (*RequestWrapper, error) {
	hj, ok := writer.(http.Hijacker)
	if !ok {
		return nil, errors.New("http server does not support hijacking")
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(nil)
	if err = req.Write(buf); err != nil {
		var bodyErr error
		if req.Body != nil {
			bodyErr = req.Body.Close()
		}
		return nil, errors.Join(err, bodyErr, conn.Close())
	}
	return &RequestWrapper{
		req:       req,
		reader:    buf,
		writer:    conn,
		closeDone: make(chan struct{}),
	}, nil
}

func (rw *RequestWrapper) Read(p []byte) (int, error) {
	count, err := rw.reader.Read(p)
	if err == nil {
		return count, nil
	}
	if err != io.EOF {
		return count, err
	}
	// request 数据读完之后等待客户端断开连接或 grpc 超时
	return rw.writer.Read(p)
}

func (rw *RequestWrapper) Write(p []byte) (int, error) {
	return rw.writer.Write(p)
}

func (rw *RequestWrapper) Close() error {
	rw.closeInit.Do(func() {
		rw.closeDone = make(chan struct{})
	})
	rw.closeOnce.Do(func() {
		var bodyErr error
		if rw.req.Body != nil {
			bodyErr = rw.req.Body.Close()
		}
		rw.closeErr = errors.Join(bodyErr, rw.writer.Close())
		close(rw.closeDone)
	})
	<-rw.closeDone
	return rw.closeErr
}
