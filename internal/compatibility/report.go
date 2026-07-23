package compatibility

type Report struct {
	Schema                 string   `json:"schema"`
	BinaryVersion          string   `json:"binary_version"`
	BinaryCommit           string   `json:"binary_commit"`
	Scope                  string   `json:"scope"`
	UnknownCommandStatus   Status   `json:"unknown_command_status"`
	GateApplied            bool     `json:"gate_applied"`
	Compatible             *bool    `json:"compatible"`
	ArgumentReviewRequired *bool    `json:"argument_review_required"`
	LimitationsAccepted    bool     `json:"limitations_accepted"`
	ArgumentReviewCommands []string `json:"argument_review_commands,omitempty"`
	Commands               []Entry  `json:"commands"`
}

func BuildReport(binaryVersion, binaryCommit string, commands []string) Report {
	return BuildReportWithOptions(binaryVersion, binaryCommit, commands, ReportOptions{})
}

type ReportOptions struct {
	AcceptLimitations bool
}

func BuildReportWithOptions(binaryVersion, binaryCommit string, commands []string, options ReportOptions) Report {
	report := Report{
		Schema:               SchemaVersion,
		BinaryVersion:        binaryVersion,
		BinaryCommit:         binaryCommit,
		Scope:                "catalog",
		UnknownCommandStatus: StatusUnsupported,
		LimitationsAccepted:  options.AcceptLimitations,
		Commands:             Catalog(),
	}
	if len(commands) == 0 {
		return report
	}

	report.Scope = "selection"
	report.GateApplied = true
	report.Commands = make([]Entry, 0, len(commands))
	compatible := true
	argumentReviewRequired := false
	for _, command := range commands {
		entry := Lookup(command)
		report.Commands = append(report.Commands, entry)
		if !entry.Compatible {
			compatible = false
		}
		if entry.ArgumentReviewRequired {
			argumentReviewRequired = true
			report.ArgumentReviewCommands = append(report.ArgumentReviewCommands, entry.Command)
			if !options.AcceptLimitations {
				compatible = false
			}
		}
	}
	report.Compatible = &compatible
	report.ArgumentReviewRequired = &argumentReviewRequired
	return report
}

func (r Report) IncompatibleCommands() []string {
	commands := make([]string, 0)
	for _, entry := range r.Commands {
		if !entry.Compatible {
			commands = append(commands, entry.Command)
		}
	}
	return commands
}

func (r Report) UnacceptedArgumentReviewCommands() []string {
	if r.LimitationsAccepted {
		return nil
	}
	commands := make([]string, len(r.ArgumentReviewCommands))
	copy(commands, r.ArgumentReviewCommands)
	return commands
}
