package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/wasteland"
	"github.com/steveyegge/gastown/internal/workspace"
)

var wlClaimPRMode bool

var wlClaimCmd = &cobra.Command{
	Use:   "claim <wanted-id>",
	Short: "Claim a wanted item",
	Long: `Claim a wanted item on the shared wanted board.

Updates the wanted row: claimed_by=<your rig handle>, status='claimed'.
The item must exist and have status='open'.

In wild-west mode (Phase 1), this writes directly to the local wl-commons
database. With --pr-mode (Phase 2), this creates a DoltHub PR instead of
writing directly, enabling review and trust-based auto-merge.

Examples:
  gt wl claim w-abc123
  gt wl claim w-abc123 --pr-mode`,
	Args: cobra.ExactArgs(1),
	RunE: runWlClaim,
}

func init() {
	wlClaimCmd.Flags().BoolVar(&wlClaimPRMode, "pr-mode", false, "Create a DoltHub PR instead of writing directly (Phase 2)")
	wlCmd.AddCommand(wlClaimCmd)
}

func runWlClaim(cmd *cobra.Command, args []string) error {
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

	if wlClaimPRMode {
		return runWlClaimPR(store, wlCfg, wantedID, rigHandle)
	}

	item, err := claimWanted(store, wantedID, rigHandle)
	if err != nil {
		return err
	}

	fmt.Printf("%s Claimed %s\n", style.Bold.Render("✓"), wantedID)
	fmt.Printf("  Claimed by: %s\n", rigHandle)
	fmt.Printf("  Title: %s\n", item.Title)

	return nil
}

// claimWanted contains the testable business logic for claiming a wanted item.
// The returned WantedItem reflects pre-claim state (status "open", empty ClaimedBy);
// callers needing post-claim state should re-query.
func claimWanted(store doltserver.WLCommonsStore, wantedID, rigHandle string) (*doltserver.WantedItem, error) {
	item, err := store.QueryWanted(wantedID)
	if err != nil {
		return nil, fmt.Errorf("querying wanted item: %w", err)
	}

	if item.Status != "open" {
		return nil, fmt.Errorf("wanted item %s is not open (status: %s)", wantedID, item.Status)
	}

	if err := store.ClaimWanted(wantedID, rigHandle); err != nil {
		return nil, fmt.Errorf("claiming wanted item: %w", err)
	}

	return item, nil
}

// runWlClaimPR implements the Phase 2 PR-based claim flow:
// 1. Verify item is open
// 2. Create a Dolt branch, write claim, commit, push
// 3. Create a DoltHub PR from the fork branch to upstream main
func runWlClaimPR(store doltserver.WLCommonsStore, wlCfg *wasteland.Config, wantedID, rigHandle string) error {
	item, err := store.QueryWanted(wantedID)
	if err != nil {
		return fmt.Errorf("querying wanted item: %w", err)
	}
	if item.Status != "open" {
		return fmt.Errorf("wanted item %s is not open (status: %s)", wantedID, item.Status)
	}

	branchName := fmt.Sprintf("wl/%s/claim-%s", rigHandle, wantedID)
	token := wasteland.GetDoltHubToken()
	if token == "" {
		return fmt.Errorf("DOLTHUB_TOKEN is required for --pr-mode")
	}

	fmt.Printf("Creating branch %s...\n", branchName)
	if err := doltserver.ClaimWantedOnBranch(wlCfg.LocalDir, wantedID, rigHandle, branchName); err != nil {
		return fmt.Errorf("branch claim failed: %w", err)
	}

	fmt.Printf("Pushing branch to %s/%s...\n", wlCfg.ForkOrg, wlCfg.ForkDB)
	if err := wasteland.PushBranch(wlCfg.LocalDir, "origin", branchName); err != nil {
		return fmt.Errorf("push failed: %w", err)
	}

	upstreamOrg, upstreamDB, err := wasteland.ParseUpstream(wlCfg.Upstream)
	if err != nil {
		return fmt.Errorf("parsing upstream: %w", err)
	}

	title := fmt.Sprintf("wl claim: %s by %s", wantedID, rigHandle)
	description := fmt.Sprintf("Claiming wanted item %s (%s)", wantedID, item.Title)

	fmt.Printf("Creating DoltHub PR: %s -> %s/%s:main...\n", branchName, upstreamOrg, upstreamDB)
	prURL, err := wasteland.CreateDoltHubPR(upstreamOrg, upstreamDB, wlCfg.ForkOrg, branchName, "main", title, description, token)
	if err != nil {
		return fmt.Errorf("DoltHub PR creation failed: %w\nFallback: use 'gt wl claim %s' (without --pr-mode) for direct write", err, wantedID)
	}

	fmt.Printf("%s PR created for claim %s\n", style.Bold.Render("✓"), wantedID)
	fmt.Printf("  Branch: %s\n", branchName)
	fmt.Printf("  PR: %s\n", prURL)
	fmt.Printf("  Awaiting merge (trust-tier dependent)\n")

	return nil
}
