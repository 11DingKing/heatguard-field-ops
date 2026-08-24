package worker

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
)

func TestWorkerAcceptsDynamicHandlersSafely(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner := New(nil, "registry-race", time.Millisecond, time.Second, 4, logger, time.Now)
	stable := func(context.Context, domain.WorkerJob) error { return nil }
	runner.Register("stable", stable)
	if runner.handlerFor("stable") == nil {
		t.Fatal("statically registered handler was not available")
	}

	start := make(chan struct{})
	var group sync.WaitGroup
	for writer := 0; writer < 4; writer++ {
		writer := writer
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for iteration := 0; iteration < 4000; iteration++ {
				runner.Register("dynamic-"+strconv.Itoa(writer)+"-"+strconv.Itoa(iteration%16), stable)
			}
		}()
	}
	for reader := 0; reader < 8; reader++ {
		reader := reader
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for iteration := 0; iteration < 8000; iteration++ {
				_ = runner.handlerFor("dynamic-" + strconv.Itoa(iteration%4) + "-" + strconv.Itoa((iteration+reader)%16))
			}
		}()
	}
	close(start)
	group.Wait()

	if runner.handlerFor("stable") == nil {
		t.Fatal("dynamic registration corrupted the existing handler")
	}
}
