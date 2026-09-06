package worker

import (
	"context"
	"fmt"
	"os"

	"github.com/TolgaOk/nextask/internal/db"
)

func (w *Worker) processTask(ctx context.Context, runCancel context.CancelFunc, notifier *db.Notifier, toWorkerCh string, task *db.Task) error {
	fmt.Printf("Processing %s: %s\n", task.ID, task.Command)

	taskCtx, taskCancel := context.WithCancel(ctx)
	defer taskCancel()

	finish := func(result *ExitResult, cancelled bool) error {
		saved := make(chan error, 1)
		go func() { saved <- w.finishTask(ctx, task, result, cancelled) }()
		// Completion retries must still respond to a worker-stop notification.
		for {
			select {
			case err := <-saved:
				return err
			case notif, ok := <-notifier.C:
				if !ok || notif.Channel == toWorkerCh {
					runCancel()
					return <-saved
				}
			case <-ctx.Done():
				return <-saved
			}
		}
	}

	// Subscribe to task cancel channel on the existing connection
	toTaskCh := db.ToTaskChannel(task.ID)
	if err := notifier.Add(taskCtx, toTaskCh); err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen for cancel: %v\n", err)
		return finish(&ExitResult{Code: -1, Err: err}, false)
	}
	defer notifier.Remove(toTaskCh)

	// Run executor in background
	resultCh := make(chan *ExitResult, 1)
	go func() {
		resultCh <- w.Executor.Execute(taskCtx, task)
	}()

	// Dispatch notifications during execution
	var result *ExitResult
	wasCancelled := false

	for {
		select {
		case result = <-resultCh:
			goto finish

		case notif, ok := <-notifier.C:
			if !ok {
				taskCancel()
				result = <-resultCh
				goto finish
			}
			switch notif.Channel {
			case toTaskCh:
				eventType, _, err := db.ParseEvent(notif.Payload)
				if err == nil && eventType == db.EventTypeCancel {
					wasCancelled = true
					taskCancel()
					result = <-resultCh
					goto finish
				}
			case toWorkerCh:
				fmt.Println("Received stop signal, shutting down...")
				runCancel()
				taskCancel()
				result = <-resultCh
				goto finish
			}

		case <-ctx.Done():
			result = <-resultCh
			goto finish
		}
	}

finish:
	return finish(result, wasCancelled)
}
