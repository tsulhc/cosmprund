package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func testRootCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "prune"}
	flags := cmd.Flags()
	flags.Uint64Var(&appBatchVersions, "app-batch-versions", 1000, "")
	flags.Uint64Var(&compactEveryBatches, "compact-every-batches", 1, "")
	flags.Uint64Var(&minFreeDiskGB, "min-free-gb", 20, "")
	flags.StringVar(&profile, "profile", "", "")
	flags.StringVar(&app, "app", "", "")
	return cmd
}

func resetVars() {
	appBatchVersions = 1000
	compactEveryBatches = 1
	minFreeDiskGB = 20
	profile = ""
	app = ""
}

func parse(cmd *cobra.Command, args ...string) {
	cmd.SetArgs(args)
	_ = cmd.Execute()
}

// --- Min free disk ---

func TestBabylonProfileMinFreeGBDefault(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon")
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 100 {
		t.Errorf("expected 100, got %d", opts.MinFreeGB)
	}
}

func TestBabylonProfileMinFreeGBExplicit20(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--min-free-gb=20")
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 20 {
		t.Errorf("expected 20, got %d", opts.MinFreeGB)
	}
}

func TestBabylonProfileMinFreeGBExplicit0(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--min-free-gb=0")
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 0 {
		t.Errorf("expected 0, got %d", opts.MinFreeGB)
	}
}

func TestBabylonProfileMinFreeGBExplicit100(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--min-free-gb=100")
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 100 {
		t.Errorf("expected 100, got %d", opts.MinFreeGB)
	}
}

func TestNoProfileMinFreeGBDefault(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd)
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 20 {
		t.Errorf("expected 20, got %d", opts.MinFreeGB)
	}
}

func TestAppBabylonNoProfileMinFreeGBDefault(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--app=babylon")
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 100 {
		t.Errorf("expected 100, got %d", opts.MinFreeGB)
	}
}

func TestAppBabylonNoProfileMinFreeGBExplicit(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--app=babylon", "--min-free-gb=20")
	opts := pruneOptionsFromFlags(cmd)
	if opts.MinFreeGB != 20 {
		t.Errorf("expected 20, got %d", opts.MinFreeGB)
	}
}

// --- App batch versions ---

func TestBabylonBatchVersionsDefault(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon")
	opts := pruneOptionsFromFlags(cmd)
	if opts.AppBatchVersions != 50 {
		t.Errorf("expected 50, got %d", opts.AppBatchVersions)
	}
}

func TestBabylonBatchVersionsExplicit1000(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--app-batch-versions=1000")
	opts := pruneOptionsFromFlags(cmd)
	if opts.AppBatchVersions != 1000 {
		t.Errorf("expected 1000, got %d", opts.AppBatchVersions)
	}
}

func TestBabylonBatchVersionsExplicit25(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--app-batch-versions=25")
	opts := pruneOptionsFromFlags(cmd)
	if opts.AppBatchVersions != 25 {
		t.Errorf("expected 25, got %d", opts.AppBatchVersions)
	}
}

// --- Compact every batches ---

func TestBabylonCompactDefault(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon")
	opts := pruneOptionsFromFlags(cmd)
	if opts.CompactEveryBatches != 0 {
		t.Errorf("expected 0, got %d", opts.CompactEveryBatches)
	}
}

func TestBabylonCompactExplicit1(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--compact-every-batches=1")
	opts := pruneOptionsFromFlags(cmd)
	if opts.CompactEveryBatches != 1 {
		t.Errorf("expected 1, got %d", opts.CompactEveryBatches)
	}
}

func TestBabylonCompactExplicit5(t *testing.T) {
	resetVars()
	cmd := testRootCmd()
	parse(cmd, "--profile=babylon", "--compact-every-batches=5")
	opts := pruneOptionsFromFlags(cmd)
	if opts.CompactEveryBatches != 5 {
		t.Errorf("expected 5, got %d", opts.CompactEveryBatches)
	}
}
