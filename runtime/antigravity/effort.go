package antigravity

import (
	"context"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/models"
)

// resolveModel returns the agy model a turn sets (spec 76 REQ-MES-005). agy
// lists one model per effort ("gemini-4-neon-low" … "-xhigh"); lana offers
// the base model ("gemini-4-neon") with an effort beside it. For a base model
// the turn picks the variant "<base>-<effort>" when agy lists it, else the
// base's default variant ("high" when listed, else the first). A full variant
// address wins over the effort; a model without variants, and "" or
// "default", are kept as they are. The list is the account's cached `agy
// models` answer, read once when the cache is empty; when it cannot be read
// the model is kept as it is.
func (r *Runtime) resolveModel(ctx context.Context, cfg core.RuntimeConfig, bin string) string {
	model := cfg.Model
	if model == "" || model == "default" {
		return model
	}
	if _, _, ok := models.SplitEffortVariant(model); ok {
		return model
	}
	account := accountName(cfg)
	list, ok := r.Models(account)
	if !ok {
		var err error
		list, _, err = r.accountModels(ctx, core.RuntimeAccount{Name: account, Home: cfg.Home, Binary: bin}, bin)
		if err != nil {
			r.log.Warn("antigravity: list models for the effort variant", "account", account, "error", err)
		}
	}
	return pickVariant(model, cfg.Effort, list)
}

// pickVariant picks the variant of base for effort among the listed models:
// "<base>-<effort>" when listed, else the base's default variant. A base that
// agy lists itself, or that has no variants, is returned as it is.
func pickVariant(base, effort string, list []core.RuntimeModel) string {
	var levels []string
	for _, m := range list {
		if m.Value == base {
			return base
		}
		if b, level, ok := models.SplitEffortVariant(m.Value); ok && b == base {
			levels = append(levels, level)
		}
	}
	if len(levels) == 0 {
		return base
	}
	levels = models.OrderVariantEfforts(levels)
	level := models.PickEffort(strings.TrimSpace(effort), levels, models.VariantDefault(levels))
	return base + "-" + level
}
