package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/slizendb/slizen/internal/service"
)

func TestAuditCommandUsesDefaultLimitAndPrintsJSON(t *testing.T) {
	requestedURL := ""
	get := func(url string) (any, error) {
		requestedURL = url
		return map[string]any{"schema_version": service.AuditSchemaVersion, "entries": []any{}}, nil
	}
	var stdout, stderr bytes.Buffer
	if err := auditCmdWithGet([]string{"--admin", "http://admin.test/"}, &stdout, &stderr, get); err != nil {
		t.Fatal(err)
	}
	if requestedURL != "http://admin.test/v1/audit?limit=100" {
		t.Fatalf("URL = %q", requestedURL)
	}
	var output map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("invalid JSON output %q: %v", stdout.String(), err)
	}
	if output["schema_version"] != service.AuditSchemaVersion {
		t.Fatalf("output = %v", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAuditCommandForwardsLimit(t *testing.T) {
	requestedURL := ""
	get := func(url string) (any, error) {
		requestedURL = url
		return map[string]any{"entries": []any{}}, nil
	}
	var stdout, stderr bytes.Buffer
	if err := auditCmdWithGet([]string{"--admin", "http://admin.test", "--limit", "7"}, &stdout, &stderr, get); err != nil {
		t.Fatal(err)
	}
	if requestedURL != "http://admin.test/v1/audit?limit=7" {
		t.Fatalf("URL = %q", requestedURL)
	}
}

func TestAuditCommandRejectsUnboundedLimits(t *testing.T) {
	for _, limit := range []string{"0", "-1", "1001"} {
		t.Run(limit, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			called := false
			get := func(string) (any, error) {
				called = true
				return nil, nil
			}
			err := auditCmdWithGet([]string{"--limit", limit}, &stdout, &stderr, get)
			if err == nil || err.Error() != "limit must be between 1 and 1000" {
				t.Fatalf("error = %v", err)
			}
			if called {
				t.Fatal("invalid limit made an admin request")
			}
		})
	}
}

func TestRunDispatchesAuditCommandBeforeMakingRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"audit", "--limit", "0"}, &stdout, &stderr)
	if err == nil || err.Error() != "limit must be between 1 and 1000" {
		t.Fatalf("error = %v", err)
	}
}

func TestHotkeysCommandRejectsUnboundedLimitsBeforeRequest(t *testing.T) {
	for _, limit := range []string{"0", "-1", "1001"} {
		var stdout, stderr bytes.Buffer
		err := hotkeysCmd([]string{"--admin", "://invalid", "--limit", limit}, &stdout, &stderr)
		if err == nil || err.Error() != "limit must be between 1 and 1000" {
			t.Fatalf("limit %s error = %v", limit, err)
		}
	}
}
