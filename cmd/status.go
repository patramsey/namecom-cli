package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/api/gen"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show a quick overview of your name.com account",
	Long:  `Displays domain counts, expiry alerts, and pending transfers at a glance.`,
	Example: `  namecom status
  namecom status --profile staging`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

type statusSummary struct {
	Profile          string    `json:"profile"`
	Endpoint         string    `json:"endpoint"`
	DomainsTotal     int       `json:"domains_total"`
	ExpiringCritical int       `json:"expiring_critical"` // <7 days
	ExpiringSoon     int       `json:"expiring_soon"`     // 7-30 days
	Unlocked         int       `json:"unlocked"`
	PendingTransfers int       `json:"pending_transfers"`
	ExpiringDomains  []expiryItem  `json:"expiring_domains,omitempty"`
	PendingDomains   []string  `json:"pending_transfer_domains,omitempty"`
}

type expiryItem struct {
	Domain  string `json:"domain"`
	Expires string `json:"expires"`
	Days    int    `json:"days"`
}

func runStatus(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)
	client := cmdutil.APIClient(cmd)
	ctx := cmd.Context()

	stop := out.Spin("Fetching account status…")

	// Fetch all domains (auto-page).
	var domains []gen.DomainResponsePayload
	var page int32 = 1
	for {
		params := &gen.ListDomainsParams{Page: &page}
		resp, err := client.Gen().ListDomains(ctx, params)
		if err != nil {
			stop()
			return err
		}
		var result gen.ListDomainsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			stop()
			return err
		}
		domains = append(domains, result.Domains...)
		if result.NextPage == nil || *result.NextPage == 0 {
			break
		}
		page = *result.NextPage
	}

	// Fetch all transfer pages for an accurate pending count.
	var transfers []gen.Transfer
	for tPage := ptrInt32(1); ; {
		tResp, err := client.Gen().ListTransfers(ctx, &gen.ListTransfersParams{Page: tPage})
		if err != nil {
			break
		}
		var tResult gen.ListTransfersResponseSchema
		if api.Decode(tResp, &tResult) != nil {
			break
		}
		transfers = append(transfers, tResult.Transfers...)
		if tResult.NextPage == nil || *tResult.NextPage == 0 {
			break
		}
		tPage = tResult.NextPage
	}

	stop()

	// Compute stats.
	now := time.Now()
	var expCritical, expSoon, unlocked int
	var expiringItems []expiryItem
	for _, d := range domains {
		if !bool(d.Locked) {
			unlocked++
		}
		if d.ExpireDate == nil {
			continue
		}
		days := int(d.ExpireDate.Sub(now).Hours() / 24)
		if days < 7 {
			expCritical++
			expiringItems = append(expiringItems, expiryItem{d.DomainName, d.ExpireDate.Format("2006-01-02"), days})
		} else if days < 30 {
			expSoon++
			expiringItems = append(expiringItems, expiryItem{d.DomainName, d.ExpireDate.Format("2006-01-02"), days})
		}
	}

	var pendingDomains []string
	for _, t := range transfers {
		s := string(t.Status)
		if s != "completed" && s != "canceled" && s != "failed" && s != "rejected" {
			pendingDomains = append(pendingDomains, t.DomainName)
		}
	}

	ov := cmdutil.Overrides(cmd)
	cfgFile := cmdutil.CfgFile(cmd)
	profileName := cfgFile.Default
	if profileName == "" {
		profileName = "default"
	}
	if ov.Profile != "" {
		profileName = ov.Profile
	}

	summary := statusSummary{
		Profile:          profileName,
		Endpoint:         client.BaseURL(),
		DomainsTotal:     len(domains),
		ExpiringCritical: expCritical,
		ExpiringSoon:     expSoon,
		Unlocked:         unlocked,
		PendingTransfers: len(pendingDomains),
		ExpiringDomains:  expiringItems,
		PendingDomains:   pendingDomains,
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(summary)
	case output.FormatYAML:
		return out.YAML(summary)
	default:
		renderStatus(out, summary)
	}
	return nil
}

func renderStatus(out *output.Config, s statusSummary) {
	// Header line: profile + endpoint.
	fmt.Fprintf(out.Writer, "%s%s  %s  %s\n",
		out.SandboxTag(),
		out.Dim("Profile"),
		s.Profile,
		out.Dim(s.Endpoint),
	)

	// Domain summary line.
	total := out.Dim(strconv.Itoa(s.DomainsTotal) + " domains")
	expPart := ""
	if s.ExpiringCritical > 0 {
		expPart = "  " + out.Red(strconv.Itoa(s.ExpiringCritical)+" expiring within 7 days")
	} else if s.ExpiringSoon > 0 {
		expPart = "  " + out.Amber(strconv.Itoa(s.ExpiringSoon)+" expiring within 30 days")
	}
	transferPart := ""
	if s.PendingTransfers > 0 {
		transferPart = "  " + out.Amber(strconv.Itoa(s.PendingTransfers)+" transfer pending")
	}
	unlockedPart := ""
	if s.Unlocked > 0 {
		unlockedPart = "  " + out.Dim(strconv.Itoa(s.Unlocked)+" unlocked")
	}
	fmt.Fprintf(out.Writer, "%s%s%s%s\n", total, expPart, transferPart, unlockedPart)

	// Expiring domains section.
	if len(s.ExpiringDomains) > 0 {
		fmt.Fprintln(out.Writer)
		fmt.Fprintln(out.Writer, "Expiring soon")
		for _, e := range s.ExpiringDomains {
			days := fmt.Sprintf("(%d days)", e.Days)
			if e.Days < 7 {
				days = out.Red(days)
			} else {
				days = out.Amber(days)
			}
			fmt.Fprintf(out.Writer, "  %-30s %s  %s\n", e.Domain, e.Expires, days)
		}
	}

	// Pending transfers section.
	if len(s.PendingDomains) > 0 {
		fmt.Fprintln(out.Writer)
		fmt.Fprintln(out.Writer, "Transfers in progress")
		for _, d := range s.PendingDomains {
			fmt.Fprintf(out.Writer, "  %s\n", d)
		}
	}

	// Footer hints.
	fmt.Fprintln(out.Writer)
	if s.ExpiringCritical > 0 || s.ExpiringSoon > 0 {
		out.Hint("Run 'namecom domain renew <domain>' to renew expiring domains")
	}
	out.Hint("Run 'namecom domain list' to see all domains")
}


func ptrInt32(n int32) *int32 { return &n }
