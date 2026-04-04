package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

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

	if len(assets) == 0 {
		fmt.Println("No assets found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tVALUE\tSTATUS\tFIRST SEEN\tLAST SEEN")
	for _, a := range assets {
		value := a.Value
		if len(value) > 60 {
			value = value[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			a.Type, value, a.Status,
			a.FirstSeen.Format("2006-01-02 15:04:05"),
			a.LastSeen.Format("2006-01-02 15:04:05"),
		)
	}
	w.Flush()
	fmt.Printf("\nShowing %d assets. Use --limit to see more.\n", len(assets))

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
	if lastScan == nil {
		fmt.Println("No scans found. Run `surfbot scan` first.")
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
		fmt.Println("No changes detected in last scan.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tVALUE\tCHANGE\tSIGNIFICANCE\tSUMMARY")
	for _, c := range changes {
		if c.Baseline {
			continue
		}
		value := c.AssetValue
		if len(value) > 50 {
			value = value[:47] + "..."
		}
		summary := c.Summary
		if len(summary) > 60 {
			summary = summary[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			c.AssetType, value,
			strings.ToUpper(string(c.ChangeType)),
			c.Significance,
			summary,
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
