// Package processing provides canonical KPI formulas for the food-processing
// module of the SIH26234 platform (spec D3 / D9).
//
// Reference example (from spec D3):
//
//	raw=500 kg, output=460 kg, waste=40 kg, downtime=35 min, runtime=420 min,
//	energy=125 kWh
//	→ material_loss_pct = 8.00 %
//	→ downtime_pct      = 8.33 %
//	→ energy_per_kg     = 0.272 kWh/kg
//	→ efficiency_score  = 77.9
package processing

import (
	"errors"
	"math"
)

// ProcessingResult holds every KPI derived from a single processing run.
type ProcessingResult struct {
	// MaterialLossPct is the percentage of raw material that did not become
	// usable output (includes process waste).
	MaterialLossPct float64

	// DowntimePct is the proportion of total runtime that was unproductive.
	DowntimePct float64

	// EnergyPerKg is the energy consumed (kWh) per kg of output produced.
	EnergyPerKg float64

	// EfficiencyScore is a composite 0–100 score.
	// Formula: clip(100 - 1.2*materialLossPct - 1.5*downtimePct, 0, 100)
	EfficiencyScore float64
}

// Sentinel errors returned by Compute.
var (
	// ErrNegativeInput is returned when any input value is negative.
	ErrNegativeInput = errors.New("processing: all inputs must be non-negative")

	// ErrDowntimeExceedsRuntime is returned when downtimeMin > runtimeMin.
	ErrDowntimeExceedsRuntime = errors.New("processing: downtime_min must not exceed runtime_min")

	// ErrZeroRuntime is returned when runtimeMin is zero (division by zero).
	ErrZeroRuntime = errors.New("processing: runtime_min must be > 0")

	// ErrZeroOutput is returned when outputKg is zero while energy > 0.
	ErrZeroOutput = errors.New("processing: output_kg must be > 0 to compute energy_per_kg")
)

// MaterialLossPct returns the percentage of raw material lost.
//
//	material_loss_pct = (rawMaterialKg - outputKg) / rawMaterialKg * 100
//
// Returns 0 when rawMaterialKg is 0.
func MaterialLossPct(rawMaterialKg, outputKg, _ float64) float64 {
	if rawMaterialKg == 0 {
		return 0
	}
	return (rawMaterialKg - outputKg) / rawMaterialKg * 100
}

// DowntimePct returns the percentage of runtime that was downtime.
//
//	downtime_pct = downtimeMin / runtimeMin * 100
//
// Returns 0 when runtimeMin is 0.
func DowntimePct(downtimeMin, runtimeMin float64) float64 {
	if runtimeMin == 0 {
		return 0
	}
	return downtimeMin / runtimeMin * 100
}

// EnergyPerKg returns the energy intensity of production (kWh per kg output).
//
//	energy_per_kg = energyKwh / outputKg
//
// Returns 0 when outputKg is 0.
func EnergyPerKg(energyKwh, outputKg float64) float64 {
	if outputKg == 0 {
		return 0
	}
	return energyKwh / outputKg
}

// EfficiencyScore computes the composite efficiency score, clipped to [0, 100].
//
//	score = clip(100 - 1.2*materialLossPct - 1.5*downtimePct, 0, 100)
func EfficiencyScore(materialLossPct, downtimePct float64) float64 {
	score := 100 - 1.2*materialLossPct - 1.5*downtimePct
	return clip(score, 0, 100)
}

// Compute validates the inputs and returns a fully populated ProcessingResult.
//
// Validation rules:
//   - all inputs must be ≥ 0
//   - downtimeMin must be ≤ runtimeMin
//   - runtimeMin must be > 0
//   - outputKg must be > 0 when energyKwh > 0
func Compute(rawMaterialKg, outputKg, wasteKg, downtimeMin, runtimeMin, energyKwh float64) (ProcessingResult, error) {
	// Guard: non-negative inputs.
	for _, v := range []float64{rawMaterialKg, outputKg, wasteKg, downtimeMin, runtimeMin, energyKwh} {
		if v < 0 {
			return ProcessingResult{}, ErrNegativeInput
		}
	}
	if runtimeMin == 0 {
		return ProcessingResult{}, ErrZeroRuntime
	}
	if downtimeMin > runtimeMin {
		return ProcessingResult{}, ErrDowntimeExceedsRuntime
	}
	if energyKwh > 0 && outputKg == 0 {
		return ProcessingResult{}, ErrZeroOutput
	}

	mlPct := MaterialLossPct(rawMaterialKg, outputKg, wasteKg)
	dtPct := DowntimePct(downtimeMin, runtimeMin)
	epk := EnergyPerKg(energyKwh, outputKg)
	es := EfficiencyScore(mlPct, dtPct)

	return ProcessingResult{
		MaterialLossPct: round2(mlPct),
		DowntimePct:     round2(dtPct),
		EnergyPerKg:     round3(epk),
		EfficiencyScore: round1(es),
	}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────────────────────

func clip(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round1(v float64) float64 { return math.Round(v*10) / 10 }
