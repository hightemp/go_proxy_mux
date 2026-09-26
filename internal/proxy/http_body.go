package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

var errResponseBodyIdle = errors.New("upstream response body idle timeout")

type httpBodyActivityReader struct {
	reader   io.Reader
	started  time.Time
	lastRead *atomic.Int64
}

func (r httpBodyActivityReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	if n > 0 {
		r.lastRead.Store(time.Since(r.started).Nanoseconds())
	}
	return n, err
}

type httpBodyFlushingWriter struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
}

func (w httpBodyFlushingWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if err != nil || n == 0 {
		return n, err
	}
	if err := w.controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return n, err
	}
	return n, nil
}

func copyHTTPResponse(w http.ResponseWriter, response *http.Response, idleTimeout time.Duration, cancel context.CancelCauseFunc) error {
	started := time.Now()
	var lastRead atomic.Int64
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
				idleFor := time.Since(started) - time.Duration(lastRead.Load())
				if idleFor >= idleTimeout {
					cancel(errResponseBodyIdle)
					return
				}
				timer.Reset(idleTimeout - idleFor)
			}
		}
	}()
	defer func() {
		close(stop)
		<-stopped
	}()
	reader := httpBodyActivityReader{reader: response.Body, started: started, lastRead: &lastRead}
	writer := httpBodyFlushingWriter{writer: w, controller: http.NewResponseController(w)}
	_, err := io.Copy(writer, reader)
	if err != nil && errors.Is(context.Cause(response.Request.Context()), errResponseBodyIdle) {
		return errResponseBodyIdle
	}
	return err
}
