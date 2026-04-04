package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

var targetCmd = &cobra.Command{
	Use:   "target",
	Short: "Manage monitored targets",
}

var targetAddCmd = &cobra.Command{
	Use:   "add <value>",
	Short: "Add a new target",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		scope, _ := cmd.Flags().GetString("scope")
		typeStr, _ := cmd.Flags().GetString("type")

		t := &model.Target{
			Value: args[0],
			Scope: model.TargetScope(scope),
		}
		if typeStr != "" {
			t.Type = model.TargetType(typeStr)
		}

		ctx := context.Background()
		p := NewPrinter(os.Stdout)
		err := store.CreateTarget(ctx, t)
		if err != nil {
			if errors.Is(err, storage.ErrAlreadyExists) {
				p.Warn("Target already exists: %s", args[0])
				return nil
			}
			if errors.Is(err, storage.ErrInvalidTarget) {
				p.Errorf("%s", err)
				return nil
			}
			return err
		}

		p.Success("Target added: %s (%s, %s) [id: %s]", t.Value, t.Type, t.Scope, t.ID[:8])
		return nil
	},
}

var targetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all targets",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		targets, err := store.ListTargets(ctx)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(targets)
		}

		p := NewPrinter(os.Stdout)

		if len(targets) == 0 {
			p.EmptyState("No targets configured.",
				"Add one with: surfbot target add <domain|ip|cidr>")
			return nil
		}

		w := p.NewTable()
		p.Theme.Bold.Fprintln(w, "ID\tVALUE\tTYPE\tSCOPE\tLAST SCAN\tCREATED")
		p.Divider(70)
		for _, t := range targets {
			lastScan := "never"
			if t.LastScanAt != nil {
				lastScan = t.LastScanAt.Format("2006-01-02")
			}
			id := t.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Fprintf(w, "%s..\t%s\t%s\t%s\t%s\t%s\n",
				id, t.Value, t.Type, t.Scope, lastScan,
				t.CreatedAt.Format("2006-01-02"))
		}
		return w.Flush()
	},
}

var targetRemoveCmd = &cobra.Command{
	Use:     "remove <id-or-value>",
	Aliases: []string{"rm", "delete"},
	Short:   "Remove a target",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		force, _ := cmd.Flags().GetBool("force")

		// Try by ID first, then by value
		t, err := store.GetTarget(ctx, args[0])
		if errors.Is(err, storage.ErrNotFound) {
			t, err = store.GetTargetByValue(ctx, args[0])
		}
		if errors.Is(err, storage.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "Target not found: %s\n", args[0])
			return nil
		}
		if err != nil {
			return err
		}

		p := NewPrinter(os.Stdout)
		if !force {
			fmt.Printf("Remove target %s and all associated data? [y/N] ", t.Value)
			var answer string
			fmt.Scanln(&answer)
			if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
				fmt.Println("Cancelled.")
				return nil
			}
		}

		if err := store.DeleteTarget(ctx, t.ID); err != nil {
			return err
		}
		p.Theme.Warning.Fprintf(os.Stdout, "Target removed: %s\n", t.Value)
		return nil
	},
}

func init() {
	targetAddCmd.Flags().String("type", "", "Target type: domain|cidr|ip (auto-detect if omitted)")
	targetAddCmd.Flags().String("scope", "external", "Target scope: external|internal|both")
	targetRemoveCmd.Flags().Bool("force", false, "Skip confirmation")
	targetCmd.AddCommand(targetAddCmd, targetListCmd, targetRemoveCmd)
	rootCmd.AddCommand(targetCmd)
}
