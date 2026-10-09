// Package ingest parses user-uploaded history files into normalised demand
// history rows.
//
// This package deliberately contains **no forecasting logic**. It only turns a
// kitchen's exported footfall / order data into rows; the prediction itself is
// produced by the ML sidecar (internal/mlclient).
package ingest

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// MaxRows caps a single upload so a malformed or enormous file cannot exhaust
// memory or the database.
const MaxRows = 50000

// HistoryRow is one normalised observation from an uploaded file.
type HistoryRow struct {
	ObservedOn   time.Time
	DayOfWeek    *int
	MealType     *string
	Footfall     *int
	OrdersCount  *int
	FoodPrepared *float64
	FoodConsumed *float64
	WasteKg      *float64
	Raw          map[string]any
	RowIndex     int
}

// ParseResult reports what a file contained, including the rows it could not
// read. Partial success is the norm — one bad line must not discard the file.
type ParseResult struct {
	Rows          []HistoryRow
	ColumnsFound  []string
	RowsRejected  int
	RejectSamples []string
}

// columnAliases maps the many names a kitchen might use onto canonical fields.
// Matching is done on a normalised header (lowercase, non-alphanumerics removed)
// so "Head Count", "head_count" and "headcount" all resolve to footfall.
var columnAliases = map[string]string{
	"date": "date", "day": "date", "observedon": "date", "timestamp": "date",
	"dayofweek": "day_of_week", "dow": "day_of_week", "weekday": "day_of_week",

	"mealtype": "meal_type", "meal": "meal_type", "session": "meal_type",

	"footfall": "footfall", "attendance": "footfall", "headcount": "footfall",
	"diners": "footfall", "expecteddiners": "footfall", "actualdiners": "footfall",
	"people": "footfall", "count": "footfall", "visitors": "footfall",

	"orders": "orders_count", "orderscount": "orders_count", "ordercount": "orders_count",
	"tickets": "orders_count", "transactions": "orders_count", "sales": "orders_count",

	"prepared": "food_prepared_kg", "preparedkg": "food_prepared_kg",
	"foodprepared": "food_prepared_kg", "preparedqty": "food_prepared_kg",
	"production": "food_prepared_kg", "cooked": "food_prepared_kg",

	"consumed": "food_consumed_kg", "consumedkg": "food_consumed_kg",
	"foodconsumed": "food_consumed_kg", "served": "food_consumed_kg",

	"waste": "waste_kg", "wastekg": "waste_kg", "leftover": "waste_kg",
	"surplus": "waste_kg", "wastequantity": "waste_kg",
}

var dateLayouts = []string{
	"2006-01-02", "2006/01/02", "02-01-2006", "01/02/2006", "02/01/2006",
	"2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00", "02-Jan-2006", "Jan 2, 2006",
	"20060102", "01-02-2006 15:04", "2006-01-02 15:04",
}

// Parse dispatches on file extension.
//
//   - .xlsx / .xlsm  → excel
//   - .tsv           → tab-delimited
//   - .csv, .txt     → comma-delimited (sniffed for a tab if it looks TSV)
func Parse(filename string, data []byte) (*ParseResult, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".xlsx", ".xlsm", ".xltx":
		return parseExcel(data)
	case ".tsv":
		return parseDelimited(data, '\t')
	case ".csv", ".txt", "":
		return parseDelimited(data, ',')
	default:
		return nil, fmt.Errorf("unsupported file type %q: upload a .csv, .tsv, .txt or .xlsx file", ext)
	}
}

func parseDelimited(data []byte, delim rune) (*ParseResult, error) {
	text := strings.TrimPrefix(string(data), "\xEF\xBB\xBF") // strip UTF-8 BOM
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// A .txt that is actually tab-separated should still import.
	if delim == ',' && looksTabSeparated(text) {
		delim = '\t'
	}

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delim
	reader.FieldsPerRecord = -1 // rows may be ragged
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("could not read delimited file: %w", err)
	}
	return buildRows(records, delim)
}

func looksTabSeparated(text string) bool {
	firstLine := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		firstLine = text[:i]
	}
	return strings.Count(firstLine, "\t") >= 1 && !strings.Contains(firstLine, ",")
}

func parseExcel(data []byte) (*ParseResult, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not open workbook: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetList()[0]
	raw, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("could not read sheet %q: %w", sheet, err)
	}
	return buildRows(raw, ',')
}

// buildRows turns a header row plus data rows into HistoryRows.
func buildRows(records [][]string, delim rune) (*ParseResult, error) {
	result := &ParseResult{}

	headerIdx := -1
	for i, rec := range records {
		if hasContent(rec) {
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 {
		return nil, fmt.Errorf("the file is empty")
	}

	header := records[headerIdx]
	mapping := map[int]string{} // column index → canonical field
	for i, h := range header {
		canonical, ok := columnAliases[normaliseHeader(h)]
		if !ok {
			continue
		}
		// First match wins, so a duplicate alias does not shadow the real column.
		if _, taken := mapping[i]; !taken {
			mapping[i] = canonical
			result.ColumnsFound = append(result.ColumnsFound, canonical)
		}
	}
	isLegacy := false
	if len(mapping) == 0 {
		// Treat as a legacy dataset: capture all columns verbatim into Raw JSON.
		isLegacy = true
		for i, h := range header {
			mapping[i] = "legacy_" + normaliseHeader(h)
		}
		result.ColumnsFound = append(result.ColumnsFound, "legacy_unstructured")
	} else {
		sort.Strings(result.ColumnsFound)
	}

	hasDate := false
	for _, canonical := range mapping {
		if canonical == "date" {
			hasDate = true
		}
	}
	if !hasDate {
		result.ColumnsFound = append(result.ColumnsFound, "date (implied)")
		sort.Strings(result.ColumnsFound)
	}

	fallbackDate := time.Now().UTC().Truncate(24 * time.Hour)

	for idx := headerIdx + 1; idx < len(records); idx++ {
		rec := records[idx]
		if !hasContent(rec) {
			continue
		}
		if len(result.Rows) >= MaxRows {
			break
		}

		row, err := buildRow(rec, mapping, fallbackDate, len(result.Rows), isLegacy)
		if err != nil {
			result.RowsRejected++
			if len(result.RejectSamples) < 5 {
				result.RejectSamples = append(result.RejectSamples,
					fmt.Sprintf("row %d: %v", idx+1, err))
			}
			continue
		}
		result.Rows = append(result.Rows, row)
	}

	if len(result.Rows) == 0 {
		return nil, fmt.Errorf("no usable rows found (%d rejected)", result.RowsRejected)
	}
	return result, nil
}

func buildRow(rec []string, mapping map[int]string, fallbackDate time.Time, rowIndex int, isLegacy bool) (HistoryRow, error) {
	row := HistoryRow{
		ObservedOn: fallbackDate,
		Raw:        map[string]any{},
		RowIndex:   rowIndex,
	}

	for i, value := range rec {
		canonical, ok := mapping[i]
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(value)
		row.Raw[canonical] = trimmed
		if trimmed == "" {
			continue
		}

		switch canonical {
		case "date":
			parsed, err := parseDate(trimmed)
			if err != nil {
				return HistoryRow{}, fmt.Errorf("unreadable date %q", trimmed)
			}
			row.ObservedOn = parsed
		case "day_of_week":
			if n, err := strconv.Atoi(trimmed); err == nil {
				row.DayOfWeek = &n
			}
		case "meal_type":
			upper := strings.ToUpper(trimmed)
			row.MealType = &upper
		case "footfall", "orders_count":
			n, err := strconv.Atoi(trimmed)
			if err != nil || n < 0 {
				return HistoryRow{}, fmt.Errorf("%s %q is not a non-negative whole number", canonical, trimmed)
			}
			if canonical == "footfall" {
				row.Footfall = &n
			} else {
				row.OrdersCount = &n
			}
		case "food_prepared_kg", "food_consumed_kg", "waste_kg":
			f, err := strconv.ParseFloat(trimmed, 64)
			if err != nil || f < 0 {
				return HistoryRow{}, fmt.Errorf("%s %q is not a non-negative number", canonical, trimmed)
			}
			switch canonical {
			case "food_prepared_kg":
				row.FoodPrepared = &f
			case "food_consumed_kg":
				row.FoodConsumed = &f
			case "waste_kg":
				row.WasteKg = &f
			}
		}
	}

	// Derive day_of_week from the date when the file did not state it.
	if row.DayOfWeek == nil {
		dow := int(row.ObservedOn.Weekday())
		if dow == 0 { // Go: Sunday = 0; the contract uses 0 = Monday.
			dow = 6
		} else {
			dow--
		}
		row.DayOfWeek = &dow
	}

	// A row with no signal at all is noise, unless it's a legacy unstructured file.
	if !isLegacy && row.Footfall == nil && row.OrdersCount == nil &&
		row.FoodPrepared == nil && row.FoodConsumed == nil && row.WasteKg == nil {
		return HistoryRow{}, fmt.Errorf("row has no footfall, orders or quantity values")
	}
	return row, nil
}

func parseDate(v string) (time.Time, error) {
	v = strings.TrimSpace(v)
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", v)
}

func normaliseHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasContent(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}
