package main

import (
	"flag"
	"fmt"
	"html"
	"math"
	"math/big"
	"os"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/payoutsim"
)

func main() {
	var (
		scenarioPath = flag.String("scenario", "", "Path to a payout simulation YAML scenario")
		format       = flag.String("format", "text", "Output format: text or markdown")
		chartOut     = flag.String("chart-out", "", "Optional path to write an SVG payout-cadence chart")
	)
	flag.Parse()

	if strings.TrimSpace(*scenarioPath) == "" {
		fmt.Fprintln(os.Stderr, "--scenario is required")
		os.Exit(2)
	}

	sc, err := payoutsim.LoadScenario(*scenarioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load scenario: %v\n", err)
		os.Exit(1)
	}
	rep, err := payoutsim.Generate(sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate report: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*chartOut) != "" {
		if err := os.WriteFile(*chartOut, []byte(renderSVG(rep)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write chart: %v\n", err)
			os.Exit(1)
		}
	}

	switch *format {
	case "text":
		fmt.Print(renderText(rep))
	case "markdown":
		fmt.Print(renderMarkdown(rep))
	default:
		fmt.Fprintf(os.Stderr, "unknown --format %q\n", *format)
		os.Exit(2)
	}
}

func renderText(rep payoutsim.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scenario: %s\n", rep.Scenario.Name)
	fmt.Fprintf(&b, "ETH/USD assumption: %.2f\n", rep.Scenario.ETHUSD)
	fmt.Fprintf(&b, "Simulation: %.1f hours, %d trials\n\n", rep.Scenario.Simulation.Hours, rep.Scenario.Simulation.Trials)
	for _, off := range rep.Offerings {
		fmt.Fprintf(&b, "%s\n", off.Offering.Name)
		fmt.Fprintf(&b, "  capability/offering: %s / %s\n", off.Offering.CapabilityID, off.Offering.OfferingID)
		fmt.Fprintf(&b, "  price per unit: %s wei/%s\n", off.PricePerUnitWei.FloatString(2), off.Offering.WorkUnit)
		if off.TokenMix != nil {
			fmt.Fprintf(&b, "  token mix/day: input=%.0f output=%.0f output_weight=%.2f billable=%.0f\n",
				off.TokenMix.InputTokensPerDay, off.TokenMix.OutputTokensPerDay, off.TokenMix.OutputWeight, off.TokenMix.BillableTokensPerDay)
		}
		fmt.Fprintf(&b, "  billable units/hour: %.2f %s\n", off.BillableUnitsPerHour, off.Offering.WorkUnit)
		fmt.Fprintf(&b, "  billable units/request: %.2f %s\n", off.BillableUnitsPerRequest, off.Offering.WorkUnit)
		fmt.Fprintf(&b, "  configured revenue/hour: $%.4f\n", off.ConfiguredRevenuePerHourUSD)
		fmt.Fprintf(&b, "  target price floor: %s wei/%s\n", off.TargetPriceFloorWholeWeiPerUnit.String(), off.Offering.WorkUnit)
		fmt.Fprintf(&b, "  fairness ratio: %.2fx\n", off.FairnessRatio)
		fmt.Fprintf(&b, "  winning ticket face value: %s wei\n", off.FaceValueWei.String())
		fmt.Fprintf(&b, "  expected value/request: %s wei\n", off.ExpectedValuePerRequestWei.FloatString(0))
		fmt.Fprintf(&b, "  expected value/ticket: %s wei\n", off.ExpectedValuePerTicketWei.FloatString(0))
		fmt.Fprintf(&b, "  win probability/ticket: %.12f (1 in %.0f)\n", off.WinProbabilityPerTicket, off.WinOddsPerTicket)
		fmt.Fprintf(&b, "  expected wins/hour: %.4f\n", off.ExpectedWinsPerHour)
		fmt.Fprintf(&b, "  expected hours/win: %.4f\n", off.ExpectedHoursPerWin)
		fmt.Fprintf(&b, "  target hours/win: %.4f\n", off.TargetHoursPerWin)
		fmt.Fprintf(&b, "  required revenue/hour for target: $%.4f\n", off.RequiredRevenuePerHourUSD)
		fmt.Fprintf(&b, "  required units/hour at current price: %.2f %s\n", off.RequiredUnitsPerHourAtPrice, off.Offering.WorkUnit)
		fmt.Fprintf(&b, "  volume multiplier for target cadence: %.2fx\n", off.VolumeMultiplierForTarget)
		fmt.Fprintf(&b, "  expected requests/win: %.2f\n", off.ExpectedRequestsPerWin)
		fmt.Fprintf(&b, "  gateway estimated units: %d\n", off.GatewayEstimatedUnits)
		fmt.Fprintf(&b, "  gateway funded value wei: %s\n", off.GatewayFundedValueWei.String())
		fmt.Fprintf(&b, "  gateway funding / request EV: %.2fx\n", off.GatewayFundingToEVRatio)
		if off.RedemptionCostWei != nil {
			fmt.Fprintf(&b, "  estimated redemption cost wei: %s\n", off.RedemptionCostWei.String())
			fmt.Fprintf(&b, "  face value / redemption cost: %.2fx\n", off.RedemptionMargin)
		}
		if off.GatewayLiveTopupThresholdUnits > 0 || off.GatewayLiveTopupFundUnits > 0 {
			fmt.Fprintf(&b, "  gateway live top-up threshold units: %d\n", off.GatewayLiveTopupThresholdUnits)
			fmt.Fprintf(&b, "  gateway live top-up fund units: %d\n", off.GatewayLiveTopupFundUnits)
		}
		fmt.Fprintf(&b, "  monte carlo mean wins: %.4f\n", off.MonteCarlo.MeanWins)
		fmt.Fprintf(&b, "  monte carlo p50/p90/p99 hours per win: %.4f / %.4f / %.4f\n",
			off.MonteCarlo.P50HoursPerWin, off.MonteCarlo.P90HoursPerWin, off.MonteCarlo.P99HoursPerWin)
		if len(off.Hardware) > 0 {
			fmt.Fprintf(&b, "  hardware profitability:\n")
			for _, hw := range off.Hardware {
				fmt.Fprintf(&b, "    %s: revenue/hour=$%.4f cost/hour=$%.4f profit/hour=$%.4f expected wins 1d/7d=%.3f/%.3f profit 1d/7d=$%.2f/$%.2f break-even=%.2f %s/hour\n",
					hw.Name, hw.RevenuePerHourUSD, hw.HourlyCostUSD, hw.ProfitPerHourUSD,
					hw.ExpectedWins1Day, hw.ExpectedWins7Day, hw.Profit1DayUSD, hw.Profit7DayUSD,
					hw.BreakEvenUnitsPerHour, off.Offering.WorkUnit)
			}
		}
		if len(off.Warnings) > 0 {
			for _, w := range off.Warnings {
				fmt.Fprintf(&b, "  warning: %s\n", w)
			}
		}
		fmt.Fprintf(&b, "  host-config snippet:\n%s\n", indent(off.HostConfigSnippet, "    "))
	}
	return b.String()
}

func renderMarkdown(rep payoutsim.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", rep.Scenario.Name)
	fmt.Fprintf(&b, "- ETH/USD assumption: `%.2f`\n", rep.Scenario.ETHUSD)
	fmt.Fprintf(&b, "- Simulation window: %.1f hours\n", rep.Scenario.Simulation.Hours)
	fmt.Fprintf(&b, "- Monte Carlo trials: %d\n\n", rep.Scenario.Simulation.Trials)
	for _, off := range rep.Offerings {
		fmt.Fprintf(&b, "## %s\n\n", off.Offering.Name)
		fmt.Fprintf(&b, "- Capability/offering: `%s` / `%s`\n", off.Offering.CapabilityID, off.Offering.OfferingID)
		if off.TokenMix != nil {
			fmt.Fprintf(&b, "- Token mix/day: input `%.0f`, output `%.0f`, output weight `%.2f`, billable `%.0f`\n",
				off.TokenMix.InputTokensPerDay, off.TokenMix.OutputTokensPerDay, off.TokenMix.OutputWeight, off.TokenMix.BillableTokensPerDay)
		}
		fmt.Fprintf(&b, "- Billable units/hour: `%.2f %s`\n", off.BillableUnitsPerHour, off.Offering.WorkUnit)
		fmt.Fprintf(&b, "- Billable units/request: `%.2f %s`\n", off.BillableUnitsPerRequest, off.Offering.WorkUnit)
		fmt.Fprintf(&b, "- Configured revenue/hour: `$%.4f`\n", off.ConfiguredRevenuePerHourUSD)
		fmt.Fprintf(&b, "- Target price floor: `%s wei/%s`\n", off.TargetPriceFloorWholeWeiPerUnit.String(), off.Offering.WorkUnit)
		fmt.Fprintf(&b, "- Fairness ratio: `%.2fx`\n", off.FairnessRatio)
		fmt.Fprintf(&b, "- Winning ticket face value: `%s wei`\n", off.FaceValueWei.String())
		fmt.Fprintf(&b, "- Expected value/request: `%s wei`\n", off.ExpectedValuePerRequestWei.FloatString(0))
		fmt.Fprintf(&b, "- Expected value/ticket: `%s wei`\n", off.ExpectedValuePerTicketWei.FloatString(0))
		fmt.Fprintf(&b, "- Win probability/ticket: `%.12f`, about `1 in %.0f`\n", off.WinProbabilityPerTicket, off.WinOddsPerTicket)
		fmt.Fprintf(&b, "- Expected wins/hour: `%.4f`\n", off.ExpectedWinsPerHour)
		fmt.Fprintf(&b, "- Expected hours/win: `%.4f`\n", off.ExpectedHoursPerWin)
		fmt.Fprintf(&b, "- Target hours/win: `%.4f`\n", off.TargetHoursPerWin)
		fmt.Fprintf(&b, "- Required revenue/hour for target: `$%.4f`\n", off.RequiredRevenuePerHourUSD)
		fmt.Fprintf(&b, "- Required units/hour at current price: `%.2f %s`\n", off.RequiredUnitsPerHourAtPrice, off.Offering.WorkUnit)
		fmt.Fprintf(&b, "- Volume multiplier for target cadence: `%.2fx`\n", off.VolumeMultiplierForTarget)
		fmt.Fprintf(&b, "- Gateway estimated units: `%d`\n", off.GatewayEstimatedUnits)
		fmt.Fprintf(&b, "- Gateway funded value: `%s wei`\n", off.GatewayFundedValueWei.String())
		fmt.Fprintf(&b, "- Gateway funding / request EV: `%.2fx`\n", off.GatewayFundingToEVRatio)
		if off.RedemptionCostWei != nil {
			fmt.Fprintf(&b, "- Estimated redemption cost: `%s wei`\n", off.RedemptionCostWei.String())
			fmt.Fprintf(&b, "- Face value / redemption cost: `%.2fx`\n", off.RedemptionMargin)
		}
		fmt.Fprintf(&b, "- Monte Carlo wait-time p50/p90/p99: `%.4f / %.4f / %.4f hours`\n\n",
			off.MonteCarlo.P50HoursPerWin, off.MonteCarlo.P90HoursPerWin, off.MonteCarlo.P99HoursPerWin)
		if len(off.Hardware) > 0 {
			fmt.Fprintln(&b, "| Hardware | Units/hr | Revenue/hr | Cost/hr | Profit/hr | Exp wins 1d | Exp wins 7d | Profit 1d | Profit 7d | Break-even units/hr |")
			fmt.Fprintln(&b, "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
			for _, hw := range off.Hardware {
				fmt.Fprintf(&b, "| %s | %.2f | $%.4f | $%.4f | $%.4f | %.3f | %.3f | $%.2f | $%.2f | %.2f |\n",
					hw.Name, hw.UnitsPerHour, hw.RevenuePerHourUSD, hw.HourlyCostUSD, hw.ProfitPerHourUSD,
					hw.ExpectedWins1Day, hw.ExpectedWins7Day, hw.Profit1DayUSD, hw.Profit7DayUSD, hw.BreakEvenUnitsPerHour)
			}
			fmt.Fprintln(&b)
		}
		if len(off.Warnings) > 0 {
			for _, w := range off.Warnings {
				fmt.Fprintf(&b, "- Warning: %s\n", w)
			}
			fmt.Fprintln(&b)
		}
		fmt.Fprintf(&b, "```yaml\n%s```\n\n", off.HostConfigSnippet)
	}
	return b.String()
}

func renderSVG(rep payoutsim.Report) string {
	const (
		width  = 1120
		height = 700
		left   = 270
		right  = 70
		top    = 104
		rowH   = 88
	)
	plotW := width - left - right
	maxLog := 1.0
	for _, off := range rep.Offerings {
		v := math.Log10(math.Max(off.VolumeMultiplierForTarget, 1))
		if v > maxLog {
			maxLog = v
		}
	}
	if maxLog <= 0 {
		maxLog = 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", width, height, width, height)
	fmt.Fprintln(&b, `<rect width="100%" height="100%" fill="#f8fafc"/>`)
	fmt.Fprintf(&b, `<text x="%d" y="36" font-family="Inter, Arial, sans-serif" font-size="24" font-weight="700" fill="#111827">%s</text>`+"\n", left, html.EscapeString(rep.Scenario.Name))
	fmt.Fprintf(&b, `<text x="%d" y="62" font-family="Inter, Arial, sans-serif" font-size="13" fill="#475569">Viability against target payout cadence. Bars show how much more volume/revenue is needed at the configured price.</text>`+"\n", left)
	if len(rep.Offerings) > 0 {
		fmt.Fprintf(&b, `<text x="%d" y="84" font-family="Inter, Arial, sans-serif" font-size="13" fill="#475569">Target: one %.4f ETH winning payout every %.0f hours.</text>`+"\n", left, weiToETH(rep.Offerings[0].FaceValueWei), rep.Offerings[0].TargetHoursPerWin)
	}
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#cbd5e1" stroke-width="1"/>`+"\n", left, top-18, left+plotW, top-18)
	for _, tick := range []float64{1, 10, 100, 1000} {
		if math.Log10(tick) <= maxLog {
			x := left + int((math.Log10(tick)/maxLog)*float64(plotW))
			fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#cbd5e1" stroke-width="1"/>`+"\n", x, top-22, x, top-14)
			fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="11" text-anchor="middle" fill="#64748b">%.0fx</text>`+"\n", x, top-28, tick)
		}
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="11" text-anchor="end" fill="#64748b">max %.0fx</text>`+"\n", left+plotW, top-28, math.Pow(10, maxLog))

	for i, off := range rep.Offerings {
		y := top + i*rowH
		mult := math.Max(off.VolumeMultiplierForTarget, 1)
		barW := int((math.Log10(mult) / maxLog) * float64(plotW))
		if barW < 3 {
			barW = 3
		}
		color := "#059669"
		status := "viable"
		if off.VolumeMultiplierForTarget > 10 {
			color = "#dc2626"
			status = "not viable"
		} else if off.VolumeMultiplierForTarget > 2 {
			color = "#ea580c"
			status = "needs more volume"
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="13" text-anchor="end" fill="#111827">%s</text>`+"\n", left-18, y+18, html.EscapeString(shortLabel(off.Offering.Name, 28)))
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="24" rx="4" fill="%s"/>`+"\n", left, y, barW, color)
		labelX := left + barW + 10
		labelAnchor := "start"
		labelFill := "#111827"
		if labelX > width-260 {
			labelX = left + barW - 10
			labelAnchor = "end"
			labelFill = "#ffffff"
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="12" text-anchor="%s" fill="%s">%.1fx volume needed - %s</text>`+"\n", labelX, y+17, labelAnchor, labelFill, off.VolumeMultiplierForTarget, status)
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="11" fill="#475569">now: %s/win; required: %.2f %s/hr or $%.4f/hr</text>`+"\n", left, y+45, html.EscapeString(hoursLabel(off.ExpectedHoursPerWin)), off.RequiredUnitsPerHourAtPrice, html.EscapeString(off.Offering.WorkUnit), off.RequiredRevenuePerHourUSD)
	}

	legendY := height - 62
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="13" font-weight="700" fill="#111827">Policy</text>`+"\n", left, legendY)
	if len(rep.Offerings) > 0 {
		first := rep.Offerings[0]
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Inter, Arial, sans-serif" font-size="12" fill="#475569">winning face value: %s wei; redemption margin: %.1fx; sender guard: funded request EV must cover requested EV.</text>`+"\n",
			left, legendY+22, html.EscapeString(first.FaceValueWei.String()), first.RedemptionMargin)
	}
	fmt.Fprintln(&b, `</svg>`)
	return b.String()
}

func weiToETH(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	r := new(big.Rat).Quo(new(big.Rat).SetInt(wei), new(big.Rat).SetInt(big.NewInt(1_000_000_000_000_000_000)))
	f, _ := r.Float64()
	return f
}

func hoursLabel(hours float64) string {
	if hours <= 0 {
		return "no wins"
	}
	if hours < 1 {
		return fmt.Sprintf("%.0f min", hours*60)
	}
	if hours < 24 {
		return fmt.Sprintf("%.1f hr", hours)
	}
	return fmt.Sprintf("%.1f days", hours/24)
}

func shortLabel(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
