package cmd

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/wasteland"
	"github.com/steveyegge/gastown/internal/workspace"
)

var wlDoneEvidence string
var wlDonePRMode bool

var wlDoneCmd = &cobra.Command{
	Use:   "done <wanted-id>",
	Short: "Submit completion evidence for a wanted item",
	Long: `Submit completion evidence for a claimed wanted item.

Inserts a completion record and updates the wanted item status to 'in_review'.
The item must be claimed by your rig.

The --evidence flag provides the evidence URL (PR link, commit hash, etc.).

A completion ID is generated as c-<hash> where hash is derived from the
wanted ID, rig handle, and timestamp.

With --pr-mode (Phase 2), this creates a DoltHub PR instead of writing
directly, enabling review and trust-based auto-merge.

Examples:
  gt wl done w-abc123 --evidence 'https://github.com/org/repo/pull/123'
  gt wl done w-abc123 --evidence 'commit abc123def'
  gt wl done w-abc123 --evidence 'https://...' --pr-mode`,
	Args: cobra.ExactArgs(1),
	RunE: runWlDone,
}

func init() {
	wlDoneCmd.Flags().StringVar(&wlDoneEvidence, "evidence", "", "Evidence URL or description (required)")
	_ = wlDoneCmd.MarkFlagRequired("evidence")
	wlDoneCmd.Flags().BoolVar(&wlDonePRMode, "pr-mode", false, "Create a DoltHub PR instead of writing directly (Phase 2)")

	wlCmd.AddCommand(wlDoneCmd)
}

func runWlDone(cmd *cobra.Command, args []string) error {
	wantedID := args[0]

	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	wlCfg, err := wasteland.LoadConfig(townRoot)
	if err != nil {
		return fmt.Errorf("loading wasteland config: %w", err)
	}
	rigHandle := wlCfg.RigHandle

	if !doltserver.DatabaseExists(townRoot, doltserver.WLCommonsDB) {
		return fmt.Errorf("database %q not found\nJoin a wasteland first with: gt wl join <org/db>", doltserver.WLCommonsDB)
	}

	store := doltserver.NewWLCommons(townRoot)
	completionID := generateCompletionID(wantedID, rigHandle)

	if wlDonePRMode {
		return runWlDonePR(store, wlCfg, wantedID, rigHandle, wlDoneEvidence, completionID)
	}

	if err := submitDone(store, wantedID, rigHandle, wlDoneEvidence, completionID); err != nil {
		return err
	}

	fmt.Printf("%s Completion submitted for %s\n", style.Bold.Render("✓"), wantedID)
	fmt.Printf("  Completion ID: %s\n", completionID)
	fmt.Printf("  Completed by: %s\n", rigHandle)
	fmt.Printf("  Evidence: %s\n", wlDoneEvidence)
	fmt.Printf("  Status: in_review\n")

	return nil
}

// runWlDonePR implements the Phase 2 PR-based completion flow:
// 1. Verify item is claimed by this rig
// 2. Create a Dolt branch, write completion, commit, push
// 3. Create a DoltHub PR from the fork branch to upstream main
func runWlDonePR(store doltserver.WLCommonsStore, wlCfg *wasteland.Config, wantedID, rigHandle, evidence, completionID string) error {
	// Verify the item exists and is claimed by us
	item, err := store.QueryWanted(wantedID)
	if err != nil {
		return fmt.Errorf("querying wanted item: %w", err)
	}
	if item.Status != "claimed" {
		return fmt.Errorf("wanted item %s is not claimed (status: %s)", wantedID, item.Status)
	}
	if item.ClaimedBy != rigHandle {
		return fmt.Errorf("wanted item %s is claimed by %q, not %q", wantedID, item.ClaimedBy, rigHandle)
	}

	branchName := fmt.Sprintf("wl/%s/done-%s", rigHandle, wantedID)
	token := wasteland.GetDoltHubToken()
	if token == "" {
		return fmt.Errorf("DOLTHUB_TOKEN is required for --pr-mode")
	}

	// Create branch, write completion on branch, commit, push
	fmt.Printf("Creating branch %s...\n", branchName)
	if err := doltserver.SubmitCompletionOnBranch(wlCfg.LocalDir, completionID, wantedID, rigHandle, evidence, branchName); err != nil {
		return fmt.Errorf("branch completion failed: %w", err)
	}

	// Push branch to fork
	fmt.Printf("Pushing branch to %s/%s...\n", wlCfg.ForkOrg, wlCfg.ForkDB)
	if err := wasteland.PushBranch(wlCfg.LocalDir, "origin", branchName); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}

	// Create DoltHub PR
	upstreamOrg, upstreamDB, err := wasteland.ParseUpstream(wlCfg.Upstream)
	if err != nil {
		return fmt.Errorf("parsing upstream: %w", err)
	}

	title := fmt.Sprintf("wl done: %s by %s", wantedID, rigHandle)
	description := fmt.Sprintf("Completion for wanted item %s\nEvidence: %s\nCompletion ID: %s", wantedID, evidence, completionID)

	fmt.Printf("Creating DoltHub PR: %s -> %s/%s:main...\n", branchName, upstreamOrg, upstreamDB)
	prURL, err := wasteland.CreateDoltHubPR(upstreamOrg, upstreamDB, wlCfg.ForkOrg, branchName, "main", title, description, token)
	if err != nil {
		return fmt.Errorf("DoltHub PR creation failed: %w\nFallback: use 'gt wl done %s --evidence %q' (without --pr-mode) for direct write", err, wantedID, evidence)
	}

	fmt.Printf("%s PR created for completion %s\n", style.Bold.Render("✓"), wantedID)
	fmt.Printf("  Completion ID: %s\n", completionID)
	fmt.Printf("  Branch: %s\n", branchName)
	fmt.Printf("  PR: %s\n", prURL)
	fmt.Printf("  Evidence: %s\n", evidence)
	fmt.Printf("  Awaiting merge (trust-tier dependent)\n")

	return nil
}

// submitDone contains the testable business logic for submitting a completion.
func submitDone(store doltserver.WLCommonsStore, wantedID, rigHandle, evidence, completionID string) error {
	item, err := store.QueryWanted(wantedID)
	if err != nil {
		return fmt.Errorf("querying wanted item: %w", err)
	}

	if item.Status != "claimed" {
		return fmt.Errorf("wanted item %s is not claimed (status: %s)", wantedID, item.Status)
	}

	if item.ClaimedBy != rigHandle {
		return fmt.Errorf("wanted item %s is claimed by %q, not %q", wantedID, item.ClaimedBy, rigHandle)
	}

	if err := store.SubmitCompletion(completionID, wantedID, rigHandle, evidence); err != nil {
		return fmt.Errorf("submitting completion: %w", err)
	}

	return nil
}

func generateCompletionID(wantedID, rigHandle string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	h := sha256.Sum256([]byte(wantedID + "|" + rigHandle + "|" + now))
	return fmt.Sprintf("c-%x", h[:8])
}
