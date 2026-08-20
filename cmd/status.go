package cmd

import (
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

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
	Profile          string `json:"profile"`
	Endpoint         string `json:"endpoint"`
	DomainsTotal     int    `json:"domains_total"`
	ExpiringCritical int    `json:"expiring_critical"` // <7 days
	ExpiringSoon     int    `json:"expiring_soon"`     // 7-30 days
	Unlocked         int    `json:"unlocked"`
	// PendingTransfers is nil when the lookup failed, for the same reason as
	// Balance: reporting 0 asserts "no transfers are pending" on the basis of a
	// request that never succeeded, and a script gating on that acts on a fact
	// the CLI never established.
	PendingTransfers *int `json:"pending_transfers,omitempty"`
	// Balance is nil when the lookup failed. It must not default to 0:
	// rendering a failed balance as $0.00 tells the user their account is
	// empty, which is worse than telling them nothing.
	Balance         *float64     `json:"balance,omitempty"`
	ExpiringDomains []expiryItem `json:"expiring_domains,omitempty"`
	PendingDomains  []string     `json:"pending_transfer_domains,omitempty"`
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

	// Run all queries in parallel — they are independent.
	var (
		totalDomains    int32
		unlockedCount   int32
		expiringDomains []gen.DomainResponsePayload
		transfers       []gen.Transfer
		transfersOK     bool
		balance         *float64
	)

	now := time.Now()
	expireEnd := now.AddDate(0, 0, 30).Format("2006-01-02")
	falseVal := false

	g, gctx := errgroup.WithContext(ctx)

	// Single page-1 request to get TotalCount — no need to page everything.
	g.Go(func() error {
		p := int32(1)
		resp, err := client.Gen().ListDomains(gctx, &gen.ListDomainsParams{Page: &p})
		if err != nil {
			return err
		}
		var result gen.ListDomainsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			return err
		}
		totalDomains = result.TotalCount
		return nil
	})

	// Count unlocked domains via the Locked=false filter.
	g.Go(func() error {
		p := int32(1)
		resp, err := client.Gen().ListDomains(gctx, &gen.ListDomainsParams{Page: &p, Locked: &falseVal})
		if err != nil {
			return err
		}
		var result gen.ListDomainsResponseSchema
		if err := api.Decode(resp, &result); err != nil {
			return err
		}
		unlockedCount = result.TotalCount
		return nil
	})

	// Fetch only domains expiring in the next 30 days.
	g.Go(func() error {
		p := int32(1)
		for {
			params := &gen.ListDomainsParams{Page: &p, ExpireDateEnd: &expireEnd}
			resp, err := client.Gen().ListDomains(gctx, params)
			if err != nil {
				return err
			}
			var result gen.ListDomainsResponseSchema
			if err := api.Decode(resp, &result); err != nil {
				return err
			}
			expiringDomains = append(expiringDomains, result.Domains...)
			next, ok := cmdutil.NextPage(p, result.NextPage, result.LastPage)
			if !ok {
				return nil
			}
			p = next
		}
	})

	// Account balance. Non-fatal: a status view is still useful without it,
	// and every purchasing command can fail with 402 "Insufficient Funds", so
	// being able to see the balance beforehand is the point of showing it.
	g.Go(func() error {
		resp, err := client.Gen().CheckAccountBalance(gctx)
		if err != nil {
			return nil
		}
		var result gen.CheckAccountBalanceResponseSchema
		if api.Decode(resp, &result) != nil {
			return nil
		}
		balance = &result.Balance
		return nil
	})

	// Fetch transfers for pending count.
	g.Go(func() error {
		for tPage := ptrInt32(1); ; {
			tResp, err := client.Gen().ListTransfers(gctx, &gen.ListTransfersParams{Page: tPage})
			if err != nil {
				// Non-fatal: a status view is still useful without it. But leave
				// transfersOK false so the count is reported as unknown rather
				// than as zero.
				return nil
			}
			var tResult gen.ListTransfersResponseSchema
			if api.Decode(tResp, &tResult) != nil {
				return nil
			}
			transfers = append(transfers, tResult.Transfers...)
			cur := int32(0)
			if tPage != nil {
				cur = *tPage
			}
			next, ok := cmdutil.NextPage(cur, tResult.NextPage, tResult.LastPage)
			if !ok {
				transfersOK = true
				return nil
			}
			tPage = &next
		}
	})

	if err := g.Wait(); err != nil {
		stop()
		return err
	}

	stop()

	// Compute stats from the targeted results.
	var expCritical, expSoon int
	var expiringItems []expiryItem
	for _, d := range expiringDomains {
		if d.ExpireDate == nil {
			continue
		}
		days := int(d.ExpireDate.Sub(now).Hours() / 24)
		if days < 7 {
			expCritical++
			expiringItems = append(expiringItems, expiryItem{d.DomainName, d.ExpireDate.Format("2006-01-02"), days})
		} else {
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

	// Only claim a count if the fetch actually completed.
	var pendingCount *int
	if transfersOK {
		n := len(pendingDomains)
		pendingCount = &n
	}

	summary := statusSummary{
		Profile:          profileName,
		Endpoint:         client.BaseURL(),
		DomainsTotal:     int(totalDomains),
		ExpiringCritical: expCritical,
		ExpiringSoon:     expSoon,
		Unlocked:         int(unlockedCount),
		PendingTransfers: pendingCount,
		ExpiringDomains:  expiringItems,
		PendingDomains:   pendingDomains,
		Balance:          balance,
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
	if s.PendingTransfers != nil && *s.PendingTransfers > 0 {
		transferPart = "  " + out.Amber(strconv.Itoa(*s.PendingTransfers)+" transfer pending")
	}
	unlockedPart := ""
	if s.Unlocked > 0 {
		unlockedPart = "  " + out.Dim(strconv.Itoa(s.Unlocked)+" unlocked")
	}
	fmt.Fprintf(out.Writer, "%s%s%s%s\n", total, expPart, transferPart, unlockedPart)

	// Balance, only when we actually have it. A failed lookup leaves this nil
	// and prints nothing — showing "$0.00" would read as an empty account.
	if s.Balance != nil {
		fmt.Fprintf(out.Writer, "%s  %s\n", out.Dim("Balance"), fmt.Sprintf("$%.2f", *s.Balance))
	}

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
