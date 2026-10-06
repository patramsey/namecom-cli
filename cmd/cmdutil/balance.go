package cmdutil

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// balanceTimeout bounds the balance lookup. A purchase prompt waits for it,
// and the lookup is a courtesy: a slow reply must not hold up the purchase.
const balanceTimeout = 5 * time.Second

// Balance is an account-balance lookup running alongside a purchase's
// pricing lookup, so the purchase's confirmation can show what the account
// holds (#271). A purchase the balance cannot cover fails after the
// confirmation with an API error; the balance on the prompt says so first.
//
// A nil *Balance is a lookup that was never started, and Wait returns nil
// for it.
type Balance struct {
	done  chan struct{}
	value *float64
}

// StartBalance starts the balance lookup for a purchase, when something will
// show it: a --dry-run, which reports it in its quote, or a confirmation
// prompt. Under --yes, or with no terminal to ask in, there is no prompt, so
// it returns nil and makes no request. The prompt line was kept free of the
// balance for every other write because of this request's cost (#228).
func StartBalance(cmd *cobra.Command) *Balance {
	if !IsDryRun(cmd) && (IsYes(cmd) || !output.IsInteractive()) {
		return nil
	}
	return LookupBalance(cmd)
}

// LookupBalance starts the balance lookup unconditionally, for a purchase
// prompt that is not a Write's: `domain check`'s register offer, which asks
// whatever --yes says. A failed lookup, or a client the context does not
// carry, leaves the balance unknown — it never fails the command.
func LookupBalance(cmd *cobra.Command) *Balance {
	client, _ := cmd.Context().Value(KeyClient).(*api.Client)
	if client == nil {
		return nil
	}
	b := &Balance{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(cmd.Context(), balanceTimeout)
	go func() {
		defer close(b.done)
		defer cancel()
		result, err := client.SDK().AccountInfo.CheckAccountBalance(ctx)
		if err != nil || result == nil {
			return
		}
		v := result.Balance
		b.value = &v
	}()
	return b
}

// Wait returns the balance in USD, or nil when the lookup failed or was not
// started.
func (b *Balance) Wait() *float64 {
	if b == nil {
		return nil
	}
	<-b.done
	return b.value
}

// PurchaseContext is PromptContext for a purchase: the same line with the
// account balance appended, "production · profile work (acme-corp) · balance
// $120.00". When the balance is below total it warns first, on its own line
// before the prompt. It does not refuse: the account may have a card on file.
// An unknown balance is left out, and an unknown total skips the warning.
func PurchaseContext(cmd *cobra.Command, total *float64, balance *float64) string {
	return withBalance(cmd, PromptContext(cmd), total, balance)
}

func withBalance(cmd *cobra.Command, ctx string, total, balance *float64) string {
	if balance == nil {
		return ctx
	}
	// Compared in cents, as CheckMaxPrice compares.
	if total != nil && math.Round(*balance*100) < math.Round(*total*100) {
		Out(cmd).Warn(fmt.Sprintf("the account balance (%s) is less than this purchase's %s; "+
			"it will fail unless the account has another way to pay", output.Money(*balance), output.Money(*total)))
	}
	line := "balance " + output.Money(*balance)
	if ctx == "" {
		return line
	}
	return ctx + " · " + line
}
