package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/TolgaOk/nextask/internal/config"
	"github.com/TolgaOk/nextask/internal/db"
)

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, syscall.EPIPE }

func TestRenderWriteFailures(t *testing.T) {
	for _, format := range []string{"table", "json", "csv"} {
		t.Run(format, func(t *testing.T) {
			err := PrintTable(commandOutput{brokenOutput{}, io.Discard}, TableConfig{
				Headers: []string{"ID"}, Rows: [][]string{{"example"}}, JSON: format == "json", CSV: format == "csv",
			})
			if !errors.Is(err, syscall.EPIPE) {
				t.Fatalf("lost write failure: %v", err)
			}
		})
	}
	for name, render := range map[string]func() error{
		"show": func() error { return printTask(brokenOutput{}, &db.Task{ID: "example"}) },
		"log":  func() error { return printLog(brokenOutput{}, db.TaskLog{Data: "example"}) },
		"wait": func() error { return printWaitLine(brokenOutput{}, "example", db.StatusCompleted, 0) },
		"empty": func() error {
			return PrintTable(commandOutput{io.Discard, brokenOutput{}}, TableConfig{EmptyMessage: "No results"})
		},
		"pagination": func() error {
			return PrintTable(commandOutput{io.Discard, brokenOutput{}}, TableConfig{Headers: []string{"ID"}, Rows: [][]string{{"example"}}, Count: 2})
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := render(); !errors.Is(err, syscall.EPIPE) {
				t.Fatalf("lost write failure: %v", err)
			}
		})
	}
}

func TestCommandOutputIsolation(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	cfg.Worker.Workdir = t.TempDir()
	for _, id := range []string{"output-alpha", "output-omega"} {
		createWatchTask(t, pool, id, map[string]string{"output": id})
		if _, err := db.InsertLog(context.Background(), pool, id, "stdout", id); err != nil {
			t.Fatal(err)
		}
		completeWatchTask(t, pool, id, 0, false)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, id := range []string{"output-alpha", "output-omega"} {
		for _, args := range [][]string{
			{"list", "--json", "--tag", "output=" + id},
			{"list", "--csv", "--tag", "output=" + id},
			{"list", "--tag", "output=" + id},
			{"show", id}, {"log", id}, {"wait", id},
			{"worker", "--once", "--_id", id},
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				root := newRootCommand("test", func() (*config.Config, error) { return &cfg, nil })
				var output bytes.Buffer
				root.SetOut(&output)
				root.SetErr(&output)
				root.SetArgs(args)
				if err := root.ExecuteContext(ctx); err != nil {
					t.Errorf("%v: %v", args, err)
					return
				}
				other := "output-alpha"
				if id == other {
					other = "output-omega"
				}
				if !strings.Contains(output.String(), id) || strings.Contains(output.String(), other) {
					t.Errorf("%v output crossed command boundaries: %q", args, output.String())
				}
			}()
		}
	}
	wg.Wait()
}

func TestStreamWriteFailure(t *testing.T) {
	pool := setupTestDB(t)
	cfg := testConfig(t)
	createWatchTask(t, pool, "broken-stream", nil)
	if _, err := db.InsertLog(context.Background(), pool, "broken-stream", "stdout", "payload"); err != nil {
		t.Fatal(err)
	}
	completeWatchTask(t, pool, "broken-stream", 0, false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cursor, calls := 0, 0
	_, err := streamTask(ctx, cfg, io.Discard, pool, "broken-stream", &cursor, func(db.TaskLog) error {
		calls++
		return syscall.EPIPE
	})
	if !errors.Is(err, syscall.EPIPE) || calls != 1 || cursor != 0 {
		t.Fatalf("output failure was retried or consumed: calls=%d cursor=%d err=%v", calls, cursor, err)
	}
}

func TestSharedCommandWriter(t *testing.T) {
	root := NewRootCommand("test")
	var buffer bytes.Buffer
	root.SetOut(&buffer)
	root.SetErr(&buffer)
	out := outputFor(root)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, w := range []io.Writer{out.out, out.err} {
				if _, err := fmt.Fprintln(w, i); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if lines := strings.Count(buffer.String(), "\n"); lines != 40 {
		t.Fatalf("lost writes: %d", lines)
	}
}
