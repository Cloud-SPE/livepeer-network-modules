package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/payoutsim"
)

func main() {
	var (
		scenarioPath = flag.String("scenario", "", "Path to a payout simulation YAML scenario")
		format       = flag.String("format", "text", "Output format: text or markdown")
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
		fmt.Fprintf(&b, "  configured revenue/hour: $%.4f\n", off.ConfiguredRevenuePerHourUSD)
		fmt.Fprintf(&b, "  target price floor: %s wei/%s\n", off.TargetPriceFloorWholeWeiPerUnit.String(), off.Offering.WorkUnit)
		fmt.Fprintf(&b, "  fairness ratio: %.2fx\n", off.FairnessRatio)
		fmt.Fprintf(&b, "  expected wins/hour: %.4f\n", off.ExpectedWinsPerHour)
		fmt.Fprintf(&b, "  expected hours/win: %.4f\n", off.ExpectedHoursPerWin)
		fmt.Fprintf(&b, "  expected requests/win: %.2f\n", off.ExpectedRequestsPerWin)
		fmt.Fprintf(&b, "  gateway estimated units: %d\n", off.GatewayEstimatedUnits)
		fmt.Fprintf(&b, "  gateway funded value wei: %s\n", off.GatewayFundedValueWei.String())
		if off.GatewayLiveTopupThresholdUnits > 0 || off.GatewayLiveTopupFundUnits > 0 {
			fmt.Fprintf(&b, "  gateway live top-up threshold units: %d\n", off.GatewayLiveTopupThresholdUnits)
			fmt.Fprintf(&b, "  gateway live top-up fund units: %d\n", off.GatewayLiveTopupFundUnits)
		}
		fmt.Fprintf(&b, "  monte carlo mean wins: %.4f\n", off.MonteCarlo.MeanWins)
		fmt.Fprintf(&b, "  monte carlo p50/p90/p99 hours per win: %.4f / %.4f / %.4f\n",
			off.MonteCarlo.P50HoursPerWin, off.MonteCarlo.P90HoursPerWin, off.MonteCarlo.P99HoursPerWin)
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
		fmt.Fprintf(&b, "- Configured revenue/hour: `$%.4f`\n", off.ConfiguredRevenuePerHourUSD)
		fmt.Fprintf(&b, "- Target price floor: `%s wei/%s`\n", off.TargetPriceFloorWholeWeiPerUnit.String(), off.Offering.WorkUnit)
		fmt.Fprintf(&b, "- Fairness ratio: `%.2fx`\n", off.FairnessRatio)
		fmt.Fprintf(&b, "- Expected wins/hour: `%.4f`\n", off.ExpectedWinsPerHour)
		fmt.Fprintf(&b, "- Expected hours/win: `%.4f`\n", off.ExpectedHoursPerWin)
		fmt.Fprintf(&b, "- Gateway estimated units: `%d`\n", off.GatewayEstimatedUnits)
		fmt.Fprintf(&b, "- Gateway funded value: `%s wei`\n", off.GatewayFundedValueWei.String())
		fmt.Fprintf(&b, "- Monte Carlo wait-time p50/p90/p99: `%.4f / %.4f / %.4f hours`\n\n",
			off.MonteCarlo.P50HoursPerWin, off.MonteCarlo.P90HoursPerWin, off.MonteCarlo.P99HoursPerWin)
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

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n") + "\n"
}
