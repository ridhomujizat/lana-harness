package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/ridhomujizat/lana-harness/router"
)

// balanceAdapter is the adapter of the kinds that report a balance or a
// credit through an API: deepseek and openrouter (REQ-RTR-012). Other kinds
// use *Adapter, which is not a router.BalanceFetcher.
type balanceAdapter struct {
	*Adapter
}

// FetchBalance returns DeepSeek's account balance or OpenRouter's key
// credit.
func (b balanceAdapter) FetchBalance(ctx context.Context) (router.LimitUpdate, error) {
	switch b.kind {
	case kindDeepSeek:
		return b.deepseekBalance(ctx)
	case kindOpenRouter:
		return b.openRouterCredit(ctx)
	}
	return router.LimitUpdate{}, router.ErrNotSupported
}

// deepseekBalance reads GET <scheme://host>/user/balance. With several
// currencies the USD entry wins, else the first.
func (a *Adapter) deepseekBalance(ctx context.Context) (router.LimitUpdate, error) {
	base, err := url.Parse(a.cfg.BaseURL)
	if err != nil {
		return router.LimitUpdate{}, err
	}
	endpoint := base.Scheme + "://" + base.Host + "/user/balance"
	data, err := a.get(ctx, endpoint)
	if err != nil {
		return router.LimitUpdate{}, err
	}
	var payload struct {
		BalanceInfos []struct {
			Currency        string     `json:"currency"`
			TotalBalance    flexNumber `json:"total_balance"`
			GrantedBalance  flexNumber `json:"granted_balance"`
			ToppedUpBalance flexNumber `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return router.LimitUpdate{}, fmt.Errorf("deepseek balance: %w", err)
	}
	if len(payload.BalanceInfos) == 0 {
		return router.LimitUpdate{}, fmt.Errorf("deepseek balance: no balance reported")
	}
	pick := payload.BalanceInfos[0]
	for _, info := range payload.BalanceInfos {
		if strings.EqualFold(info.Currency, "USD") {
			pick = info
			break
		}
	}
	return router.LimitUpdate{Balance: &router.Balance{
		Currency: strings.ToUpper(pick.Currency),
		Total:    pick.TotalBalance.value,
		Granted:  pick.GrantedBalance.value,
		ToppedUp: pick.ToppedUpBalance.value,
	}}, nil
}

// openRouterCredit reads GET {base_url}/key. A key without a limit has nil
// LimitUSD and RemainingUSD.
func (a *Adapter) openRouterCredit(ctx context.Context) (router.LimitUpdate, error) {
	data, err := a.get(ctx, strings.TrimRight(a.cfg.BaseURL, "/")+"/key")
	if err != nil {
		return router.LimitUpdate{}, err
	}
	var payload struct {
		Data struct {
			Limit          flexNumber `json:"limit"`
			Usage          flexNumber `json:"usage"`
			LimitRemaining flexNumber `json:"limit_remaining"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return router.LimitUpdate{}, fmt.Errorf("openrouter key: %w", err)
	}
	d := payload.Data
	credit := &router.Credit{UsedUSD: d.Usage.value}
	if d.Limit.set {
		limit := d.Limit.value
		remaining := max(limit-d.Usage.value, 0)
		if d.LimitRemaining.set {
			remaining = d.LimitRemaining.value
		}
		credit.LimitUSD = &limit
		credit.RemainingUSD = &remaining
	}
	return router.LimitUpdate{Credit: credit}, nil
}
