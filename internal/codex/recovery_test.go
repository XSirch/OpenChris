package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func requestFor(prompt string) Request {
	return Request{RunID: "00000000-0000-4000-8000-000000000002", AgentID: "A01", Context: json.RawMessage(fmt.Sprintf(`{"current":%q,"sources":[{"id":"message:1"}]}`, prompt))}
}
func TestPolicyBlockSurvivesRunnerRecreation(t *testing.T) {
	r := testRunner(t, true)
	r.Run(context.Background(), requestFor("tool"))
	replacement, err := NewRunner(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.Run(context.Background(), requestFor("ok")); Code(err) != "policy_violation" {
		t.Fatal("restart removed policy block", err)
	}
}
func TestStorageFailureBlocksAnotherRunnerInstance(t *testing.T) {
	r := testRunner(t, true)
	home, err := r.home()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	r.cache.path = filepath.Join(home, "auth.json", "invalid.enc")
	if err = r.persist(home); Code(err) != "auth_storage_failed" {
		t.Fatal(err)
	}
	replacement, err := NewRunner(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Status(context.Background()).State != "auth_storage_failed" {
		t.Fatal("restart lost storage incident")
	}
}
func TestSeparateRunnerInstancesShareExclusiveAccountLock(t *testing.T) {
	r := testRunner(t, true)
	other, err := NewRunner(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	signal := filepath.Join(t.TempDir(), "started")
	request := requestFor("hold")
	request.Context = json.RawMessage(fmt.Sprintf(`{"current":"hold","signal":%q,"sources":[{"id":"message:1"}]}`, signal))
	done := make(chan error, 1)
	go func() { _, err := r.Run(context.Background(), request); done <- err }()
	defer func() { <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err = os.Stat(signal); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = other.Run(context.Background(), requestFor("ok")); Code(err) != "busy" {
		t.Fatal("concurrent account access was accepted", err)
	}
}
