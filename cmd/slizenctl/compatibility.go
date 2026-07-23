package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/slizendb/slizen/internal/buildinfo"
	"github.com/slizendb/slizen/internal/compatibility"
)

const maxCompatibilityCommands = 1024

func compatibilityCmd(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "report" {
		return errors.New("usage: slizenctl compatibility report [--output text|json] [--accept-limitations] [COMMAND ...]")
	}

	fs := flag.NewFlagSet("compatibility report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	output := fs.String("output", "text", "output format: text or json")
	acceptLimitations := fs.Bool("accept-limitations", false, "accept documented argument-level limitations for the selected commands")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *output != "text" && *output != "json" {
		return errors.New("output must be text or json")
	}
	commands := fs.Args()
	if len(commands) > maxCompatibilityCommands {
		return fmt.Errorf("at most %d commands may be checked", maxCompatibilityCommands)
	}

	report := compatibility.BuildReportWithOptions(buildinfo.Version, buildinfo.Commit, commands, compatibility.ReportOptions{
		AcceptLimitations: *acceptLimitations,
	})
	if err := writeCompatibilityReport(stdout, *output, report); err != nil {
		return err
	}
	if report.GateApplied && report.Compatible != nil && !*report.Compatible {
		return compatibilityGateError(report)
	}
	return nil
}

func compatibilityGateError(report compatibility.Report) error {
	problems := make([]string, 0, 2)
	if commands := report.IncompatibleCommands(); len(commands) > 0 {
		problems = append(problems, "incompatible commands: "+strings.Join(commands, ", "))
	}
	if commands := report.UnacceptedArgumentReviewCommands(); len(commands) > 0 {
		problems = append(problems, "commands require argument review: "+strings.Join(commands, ", ")+" (review their limitations and rerun with --accept-limitations)")
	}
	if len(problems) == 0 {
		return errors.New("compatibility gate failed")
	}
	return errors.New(strings.Join(problems, "; "))
}

func writeCompatibilityReport(w io.Writer, output string, report compatibility.Report) error {
	if output == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	if _, err := fmt.Fprintf(w, "schema: %s\nbinary_version: %s\nbinary_commit: %s\nscope: %s\nunknown_command_status: %s\n", report.Schema, report.BinaryVersion, report.BinaryCommit, report.Scope, report.UnknownCommandStatus); err != nil {
		return err
	}
	if report.GateApplied && report.Compatible != nil {
		if _, err := fmt.Fprintf(w, "compatible: %t\n", *report.Compatible); err != nil {
			return err
		}
		if report.ArgumentReviewRequired != nil {
			if _, err := fmt.Fprintf(w, "argument_review_required: %t\nlimitations_accepted: %t\n", *report.ArgumentReviewRequired, report.LimitationsAccepted); err != nil {
				return err
			}
			if len(report.ArgumentReviewCommands) > 0 {
				if _, err := fmt.Fprintf(w, "argument_review_commands: %s\n", strings.Join(report.ArgumentReviewCommands, ", ")); err != nil {
					return err
				}
			}
		}
	} else if _, err := fmt.Fprintln(w, "compatible: not evaluated (informational catalog)"); err != nil {
		return err
	}

	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "\nCOMMAND\tSTATUS\tDETAILS"); err != nil {
		return err
	}
	for _, entry := range report.Commands {
		details := entry.Behavior
		if entry.Limitations != "" {
			details += " Argument review required. Limitation: " + entry.Limitations
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\n", entry.Command, entry.Status, details); err != nil {
			return err
		}
	}
	return table.Flush()
}
