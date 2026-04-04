package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/surfbot-io/surfbot-agent/internal/model"
	"github.com/surfbot-io/surfbot-agent/internal/storage"
)

var assetsCmd = &cobra.Command{
	Use:   "assets",
	Short: "List discovered assets",
	RunE:  runAssets,
}

func init() {
	assetsCmd.Flags().String("type", "", "Filter by type: subdomain|ipv4|ipv6|port_service|url|technology|service")
	assetsCmd.Flags().Bool("new", false, "Show only new assets")
	assetsCmd.Flags().Bool("disappeared", false, "Show only disappeared assets")
	assetsCmd.Flags().Bool("diff", false, "Show changes since last scan")
	assetsCmd.Flags().Int("limit", 100, "Max number of results")
	assetsCmd.Flags().Bool("json", false, "Output as JSON")
	rootCmd.AddCommand(assetsCmd)
}

func runAssets(cmd *cobra.Command, args []string) error {
	diffMode, _ := cmd.Flags().GetBool("diff")
	if diffMode {
		return runAssetsDiff(cmd)
	}
	return runAssetsList(cmd)
}

func runAssetsList(cmd *cobra.Command) error {
	ctx := context.Background()

	newOnly, _ := cmd.Flags().GetBool("new")
	disappeared, _ := cmd.Flags().GetBool("disappeared")
	assetType, _ := cmd.Flags().GetString("type")
	limit, _ := cmd.Flags().GetInt("limit")
	asJSON, _ := cmd.Flags().GetBool("json")

	opts := storage.AssetListOptions{
		Type:        model.AssetType(assetType),
		NewOnly:     newOnly,
		Disappeared: disappeared,
		Limit:       limit,
	}

	assets, err := store.ListAssets(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing assets: %w", err)
	}

	if asJSON {
		return printAssetsJSON(assets)
	}

	p := NewPrinter(os.Stdout)

	if len(assets) == 0 {
		p.EmptyState("No assets found.",
			"Run `surfbot scan <target>` to discover assets.")
		return nil
	}

	w := p.NewTable()
	p.Theme.Bold.Fprintln(w, "TYPE\tVALUE\tSTATUS\tFIRST SEEN\tLAST SEEN")
	p.Divider(70)
	for _, a := range assets {
		value := truncate(a.Value, 50)
		statusStr := string(a.Status)
		switch a.Status {
		case model.AssetStatusNew:
			statusStr = p.Theme.Success.Sprint("new")
		case model.AssetStatusDisappeared:
			statusStr = p.Theme.Error.Sprint("disappeared")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			a.Type, value, statusStr,
			a.FirstSeen.Format("2006-01-02 15:04:05"),
			a.LastSeen.Format("2006-01-02 15:04:05"),
		)
	}
	w.Flush()
	p.Muted("\nShowing %d assets. Use --limit to see more.\n", len(assets))

	return nil
}

func runAssetsDiff(cmd *cobra.Command) error {
	ctx := context.Background()

	asJSON, _ := cmd.Flags().GetBool("json")
	limit, _ := cmd.Flags().GetInt("limit")

	lastScan, err := store.LastScan(ctx)
	if err != nil {
		return fmt.Errorf("getting last scan: %w", err)
	}

	p := NewPrinter(os.Stdout)

	if lastScan == nil {
		p.EmptyState("No scans found.",
			"Run `surfbot scan <target>` first.")
		return nil
	}

	changes, err := store.ListAssetChanges(ctx, storage.AssetChangeListOptions{
		ScanID: lastScan.ID,
		Limit:  limit,
	})
	if err != nil {
		return fmt.Errorf("listing changes: %w", err)
	}

	if asJSON {
		return printChangesJSON(changes)
	}

	if len(changes) == 0 {
		p.EmptyState("No changes detected.",
			"Run a second scan to start tracking changes.")
		return nil
	}

	w := p.NewTable()
	p.Theme.Bold.Fprintln(w, "TYPE\tVALUE\tCHANGE\tSIGNIFICANCE\tSUMMARY")
	p.Divider(70)
	for _, c := range changes {
		if c.Baseline {
			continue
		}
		value := truncate(c.AssetValue, 50)
		summary := truncate(c.Summary, 50)

		// Color the change type
		changeStr := strings.ToUpper(string(c.ChangeType))
		switch c.ChangeType {
		case model.ChangeTypeAppeared:
			changeStr = p.Theme.Success.Sprint(changeStr)
		case model.ChangeTypeDisappeared:
			changeStr = p.Theme.Error.Sprint(changeStr)
		case model.ChangeTypeModified:
			changeStr = p.Theme.Warning.Sprint(changeStr)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			c.AssetType, value, changeStr, c.Significance, summary,
		)
	}
	w.Flush()

	return nil
}

func printAssetsJSON(assets []model.Asset) error {
	data, err := json.MarshalIndent(assets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling assets: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func printChangesJSON(changes []model.AssetChange) error {
	data, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling changes: %w", err)
	}
	fmt.Println(string(data))
	return nil
}
