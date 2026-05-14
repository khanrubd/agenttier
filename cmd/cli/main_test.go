/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestParseSSE_HandlesAllEventTypes(t *testing.T) {
	body := strings.Join([]string{
		"event: start",
		`data: {"invokeId":"inv-1","startedAt":1}`,
		"",
		": keepalive",
		"",
		"event: log",
		`data: {"stream":"stdout","data":"hello"}`,
		"",
		"event: exit",
		`data: {"invokeId":"inv-1","exitCode":0,"durationMs":42,"reason":"completed"}`,
		"",
	}, "\n")

	var got []sseEvent
	for evt := range parseSSE(strings.NewReader(body)) {
		got = append(got, evt)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 events (start, log, exit), got %d", len(got))
	}
	if got[0].event != "start" {
		t.Errorf("expected start, got %q", got[0].event)
	}
	if got[0].stringField("invokeId") != "inv-1" {
		t.Errorf("expected invokeId=inv-1, got %q", got[0].stringField("invokeId"))
	}
	if got[1].dataField("data", "") != "hello" {
		t.Errorf("expected log data=hello, got %q", got[1].dataField("data", ""))
	}
	if got[2].intField("exitCode") != 0 {
		t.Errorf("expected exit code 0, got %d", got[2].intField("exitCode"))
	}
	if got[2].stringField("reason") != "completed" {
		t.Errorf("expected reason completed, got %q", got[2].stringField("reason"))
	}
}

func TestParseSSE_TrailingEventWithoutBlankLine(t *testing.T) {
	body := "event: result\ndata: {\"installExitCode\":0,\"skipped\":false}"
	var events []sseEvent
	for evt := range parseSSE(strings.NewReader(body)) {
		events = append(events, evt)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].event != "result" {
		t.Errorf("expected result, got %q", events[0].event)
	}
	if events[0].boolField("skipped") {
		t.Error("expected skipped=false")
	}
}

func TestBuildConfigurePayload_HappyPath(t *testing.T) {
	tmp := t.TempDir() + "/agent.py"
	if err := writeTempFile(tmp, "print('hi')"); err != nil {
		t.Fatalf("seed temp file: %v", err)
	}

	body, err := buildConfigurePayload(
		"pip install -r req.txt",
		"python /workspace/agent.py",
		[]string{"/workspace/agent.py=" + tmp},
	)
	if err != nil {
		t.Fatalf("buildConfigurePayload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	install, _ := decoded["installCommand"].([]any)
	if len(install) != 4 || install[0].(string) != "pip" {
		t.Errorf("expected install split, got %v", decoded["installCommand"])
	}
	entrypoint, _ := decoded["entrypoint"].([]any)
	if len(entrypoint) != 2 || entrypoint[0].(string) != "python" {
		t.Errorf("expected entrypoint split, got %v", decoded["entrypoint"])
	}
	files, _ := decoded["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	first, _ := files[0].(map[string]any)
	if first["path"] != "/workspace/agent.py" {
		t.Errorf("expected path /workspace/agent.py, got %v", first["path"])
	}
	if first["content"] != "print('hi')" {
		t.Errorf("expected text content roundtrip, got %v", first["content"])
	}
}

func TestBuildConfigurePayload_RejectsEmpty(t *testing.T) {
	if _, err := buildConfigurePayload("", "", nil); err == nil {
		t.Error("expected error when no args supplied")
	}
}

func TestBuildConfigurePayload_RejectsBadFileSpec(t *testing.T) {
	if _, err := buildConfigurePayload("", "", []string{"badspec"}); err == nil {
		t.Error("expected error for missing = in file spec")
	}
}

func TestReadInvokeBody(t *testing.T) {
	if b, _ := readInvokeBody(""); b != nil {
		t.Errorf("expected nil for empty input, got %v", b)
	}
	if b, _ := readInvokeBody("hello"); string(b) != "hello" {
		t.Errorf("expected inline passthrough, got %q", string(b))
	}
	tmp := t.TempDir() + "/payload.txt"
	if err := writeTempFile(tmp, "from-file"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if b, _ := readInvokeBody("@" + tmp); string(b) != "from-file" {
		t.Errorf("expected file content, got %q", string(b))
	}
}

// writeTempFile writes content to the path. Tiny helper so tests don't pull
// in os twice.
func writeTempFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
