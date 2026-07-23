package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/slizendb/slizen/internal/buildinfo"
	"github.com/slizendb/slizen/internal/compatibility"
)

func TestCompatibilityReportCatalogIsInformationalAndLocal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"compatibility", "report"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"schema: " + compatibility.SchemaVersion,
		"binary_version: " + buildinfo.Version,
		"binary_commit: " + buildinfo.Commit,
		"unknown_command_status: unsupported",
		"compatible: not evaluated",
		"GET",
		"supported",
		"MULTI",
		"rejected",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestCompatibilityReportJSONUsesStableSchemaAndBinaryCatalog(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := compatibilityCmd([]string{"report", "--output", "json", "get", "ttl"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	var report compatibility.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), err)
	}
	if report.Schema != compatibility.SchemaVersion || report.BinaryVersion != buildinfo.Version || report.BinaryCommit != buildinfo.Commit {
		t.Fatalf("report identity = schema:%q binary:%q commit:%q", report.Schema, report.BinaryVersion, report.BinaryCommit)
	}
	if report.UnknownCommandStatus != compatibility.StatusUnsupported {
		t.Fatalf("unknown command status = %q", report.UnknownCommandStatus)
	}
	if report.Scope != "selection" || !report.GateApplied || report.Compatible == nil || !*report.Compatible {
		t.Fatalf("report gate = scope:%q applied:%t compatible:%v", report.Scope, report.GateApplied, report.Compatible)
	}
	if got := []string{report.Commands[0].Command, report.Commands[1].Command}; got[0] != "GET" || got[1] != "TTL" {
		t.Fatalf("commands = %v", got)
	}
}

func TestCompatibilityReportFailsExplicitIncompatibleSelectionAfterPrinting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"compatibility", "report", "--output", "json", "GET", "eval", "MULTI"}, &stdout, &stderr)
	if err == nil || err.Error() != "incompatible commands: EVAL, MULTI" {
		t.Fatalf("error = %v", err)
	}

	var report compatibility.Report
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), decodeErr)
	}
	if report.Compatible == nil || *report.Compatible {
		t.Fatalf("compatible = %v", report.Compatible)
	}
	if report.Commands[1].Status != compatibility.StatusUnsupported || report.Commands[2].Status != compatibility.StatusRejected {
		t.Fatalf("commands = %+v", report.Commands)
	}
}

func TestCompatibilityReportRequiresExplicitLimitationAcceptance(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := compatibilityCmd([]string{"report", "--output", "json", "GET", "set", "SELECT"}, &stdout, &stderr)
	if err == nil || err.Error() != "commands require argument review: SET, SELECT (review their limitations and rerun with --accept-limitations)" {
		t.Fatalf("error = %v", err)
	}

	var report compatibility.Report
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), decodeErr)
	}
	if report.Compatible == nil || *report.Compatible {
		t.Fatalf("compatible = %v, want false", report.Compatible)
	}
	if report.ArgumentReviewRequired == nil || !*report.ArgumentReviewRequired || report.LimitationsAccepted {
		t.Fatalf("argument review state = required:%v accepted:%t", report.ArgumentReviewRequired, report.LimitationsAccepted)
	}
	if got, want := strings.Join(report.ArgumentReviewCommands, ","), "SET,SELECT"; got != want {
		t.Fatalf("argument review commands = %q, want %q", got, want)
	}
	if !report.Commands[1].ArgumentReviewRequired || report.Commands[1].Limitations == "" {
		t.Fatalf("SET row does not expose its limitation: %+v", report.Commands[1])
	}
	if strings.Contains(stdout.String(), `"compatible": true`) || !strings.Contains(stdout.String(), `"command_name_compatible": true`) {
		t.Fatalf("row-level compatibility semantics are ambiguous:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = compatibilityCmd([]string{"report", "--output", "json", "--accept-limitations", "GET", "SET", "SELECT"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("invalid accepted JSON %q: %v", stdout.String(), decodeErr)
	}
	if report.Compatible == nil || !*report.Compatible || !report.LimitationsAccepted {
		t.Fatalf("accepted report gate = compatible:%v accepted:%t", report.Compatible, report.LimitationsAccepted)
	}
}

func TestCompatibilityReportDoesNotLetAcceptanceBypassIncompatibleCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := compatibilityCmd([]string{"report", "--output", "json", "--accept-limitations", "SET", "EVAL"}, &stdout, &stderr)
	if err == nil || err.Error() != "incompatible commands: EVAL" {
		t.Fatalf("error = %v", err)
	}

	var report compatibility.Report
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), decodeErr)
	}
	if report.Compatible == nil || *report.Compatible {
		t.Fatalf("compatible = %v, want false", report.Compatible)
	}
}

func TestCompatibilityReportTextMakesArgumentReviewVisible(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := compatibilityCmd([]string{"report", "SET"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected limited SET to fail without explicit acceptance")
	}
	for _, want := range []string{
		"compatible: false",
		"argument_review_required: true",
		"limitations_accepted: false",
		"argument_review_commands: SET",
		"Argument review required.",
		"SET GET is rejected",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestCompatibilityReportRejectsInvalidFormatAndUnboundedSelection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := compatibilityCmd([]string{"report", "--output", "yaml"}, &stdout, &stderr); err == nil || err.Error() != "output must be text or json" {
		t.Fatalf("format error = %v", err)
	}

	commands := make([]string, maxCompatibilityCommands+1)
	for i := range commands {
		commands[i] = "GET"
	}
	args := append([]string{"report"}, commands...)
	if err := compatibilityCmd(args, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "at most 1024") {
		t.Fatalf("selection error = %v", err)
	}
}
