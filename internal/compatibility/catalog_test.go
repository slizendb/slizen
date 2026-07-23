package compatibility

import (
	"reflect"
	"strings"
	"testing"
)

func TestCatalogIsDeterministicAndSorted(t *testing.T) {
	first := Catalog()
	second := Catalog()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Catalog returned different results")
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Command >= first[i].Command {
			t.Fatalf("catalog is not strictly sorted at %q, %q", first[i-1].Command, first[i].Command)
		}
	}
}

func TestLookupIsCaseInsensitiveAndUnknownCommandsAreUnsupported(t *testing.T) {
	get := Lookup(" get ")
	if get.Command != "GET" || get.Status != StatusSupported || !get.Compatible {
		t.Fatalf("Lookup(GET) = %+v", get)
	}

	eval := Lookup("eval")
	if eval.Command != "EVAL" || eval.Status != StatusUnsupported || eval.Class != ClassUnsupported || eval.Compatible {
		t.Fatalf("Lookup(EVAL) = %+v", eval)
	}
}

func TestLookupNormalizedCommandDoesNotAllocate(t *testing.T) {
	for name, lookup := range map[string]func() Entry{
		"Lookup":           func() Entry { return Lookup("SET") },
		"LookupNormalized": func() Entry { return LookupNormalized("SET") },
	} {
		if allocations := testing.AllocsPerRun(1000, func() {
			_ = lookup()
		}); allocations != 0 {
			t.Fatalf("%s(SET) allocations = %f, want 0", name, allocations)
		}
	}
}

func TestLimitedCommandsRequireArgumentReview(t *testing.T) {
	for _, command := range []string{"SET", "SELECT", "EXPIRE", "PEXPIRE"} {
		entry := LookupNormalized(command)
		if !entry.Compatible || !entry.ArgumentReviewRequired || entry.Limitations == "" {
			t.Errorf("%s entry = %+v, want compatible command name with required argument review", command, entry)
		}
	}
	for _, command := range []string{"GET", "MGET", "TTL", "DEL"} {
		if entry := LookupNormalized(command); entry.ArgumentReviewRequired {
			t.Errorf("%s unexpectedly requires argument review: %+v", command, entry)
		}
	}
}

func TestWriteBehaviorDocumentsConservativePreDispatchInvalidation(t *testing.T) {
	for _, command := range []string{"DEL", "EXPIRE", "PERSIST", "PEXPIRE", "PSETEX", "SET", "SETEX", "UNLINK"} {
		behavior := LookupNormalized(command).Behavior
		if !strings.Contains(behavior, "before upstream dispatch") || !strings.Contains(behavior, "remain") || !strings.Contains(behavior, "ambiguous") {
			t.Errorf("%s behavior does not describe conservative invalidation: %q", command, behavior)
		}
	}
}

func TestBuildReportAppliesGateOnlyToExplicitSelection(t *testing.T) {
	catalog := BuildReport("test-version", "test-commit", nil)
	if catalog.Schema != SchemaVersion || catalog.BinaryVersion != "test-version" || catalog.BinaryCommit != "test-commit" || catalog.Scope != "catalog" {
		t.Fatalf("catalog report metadata = %+v", catalog)
	}
	if catalog.UnknownCommandStatus != StatusUnsupported {
		t.Fatalf("unknown command status = %q", catalog.UnknownCommandStatus)
	}
	if catalog.GateApplied || catalog.Compatible != nil {
		t.Fatalf("catalog gate = applied:%t compatible:%v", catalog.GateApplied, catalog.Compatible)
	}

	selection := BuildReport("test-version", "test-commit", []string{"get", "EVAL", "MULTI"})
	if !selection.GateApplied || selection.Compatible == nil || *selection.Compatible {
		t.Fatalf("selection gate = applied:%t compatible:%v", selection.GateApplied, selection.Compatible)
	}
	if got, want := selection.IncompatibleCommands(), []string{"EVAL", "MULTI"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("incompatible commands = %v, want %v", got, want)
	}
}

func TestBuildReportRequiresExplicitAcceptanceForLimitedCommands(t *testing.T) {
	selection := BuildReport("test-version", "test-commit", []string{"GET", "set", "SELECT"})
	if selection.Compatible == nil || *selection.Compatible {
		t.Fatalf("compatible = %v, want false before limitations are accepted", selection.Compatible)
	}
	if selection.ArgumentReviewRequired == nil || !*selection.ArgumentReviewRequired {
		t.Fatalf("argument review required = %v, want true", selection.ArgumentReviewRequired)
	}
	if selection.LimitationsAccepted {
		t.Fatal("limitations unexpectedly accepted")
	}
	if got, want := selection.ArgumentReviewCommands, []string{"SET", "SELECT"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("argument review commands = %v, want %v", got, want)
	}
	if got := selection.IncompatibleCommands(); len(got) != 0 {
		t.Fatalf("limited command names reported as incompatible: %v", got)
	}
	if got, want := selection.UnacceptedArgumentReviewCommands(), []string{"SET", "SELECT"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unaccepted review commands = %v, want %v", got, want)
	}

	accepted := BuildReportWithOptions("test-version", "test-commit", []string{"GET", "SET", "SELECT"}, ReportOptions{
		AcceptLimitations: true,
	})
	if accepted.Compatible == nil || !*accepted.Compatible {
		t.Fatalf("compatible = %v, want true after limitations are accepted", accepted.Compatible)
	}
	if !accepted.LimitationsAccepted || len(accepted.UnacceptedArgumentReviewCommands()) != 0 {
		t.Fatalf("accepted report did not record acceptance: %+v", accepted)
	}
}
