package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustParse(t *testing.T, name, body string) *ParseResult {
	t.Helper()
	res, err := Parse(name, []byte(body))
	if err != nil {
		t.Fatalf("Parse(%s) = %v", name, err)
	}
	return res
}

func TestParseCSVCanonicalHeaders(t *testing.T) {
	csv := "date,attendance,orders,prepared_kg,consumed_kg,waste_kg\n" +
		"2026-10-01,420,380,180.5,165.0,15.5\n" +
		"2026-10-02,450,410,190.0,178.0,12.0\n"

	res := mustParse(t, "history.csv", csv)
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}

	first := res.Rows[0]
	if first.Footfall == nil || *first.Footfall != 420 {
		t.Errorf("footfall = %v, want 420", first.Footfall)
	}
	if first.OrdersCount == nil || *first.OrdersCount != 380 {
		t.Errorf("orders = %v, want 380", first.OrdersCount)
	}
	if first.FoodPrepared == nil || *first.FoodPrepared != 180.5 {
		t.Errorf("prepared = %v, want 180.5", first.FoodPrepared)
	}
	if first.WasteKg == nil || *first.WasteKg != 15.5 {
		t.Errorf("waste = %v, want 15.5", first.WasteKg)
	}
	if first.DayOfWeek == nil {
		t.Fatal("day_of_week should be derived from the date")
	}
	if first.ObservedOn.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("date = %s, want 2026-10-01", first.ObservedOn)
	}
}

// A kitchen's export is unlikely to use our exact column names.
func TestParseCSVHeaderAliases(t *testing.T) {
	csv := "Day,Head Count,Meal,Orders Count\n" +
		"01/10/2026,300,BREAKFAST,220\n"

	res := mustParse(t, "footfall.csv", csv)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	r := res.Rows[0]
	if r.Footfall == nil || *r.Footfall != 300 {
		t.Errorf("footfall = %v, want 300", r.Footfall)
	}
	if r.OrdersCount == nil || *r.OrdersCount != 220 {
		t.Errorf("orders = %v, want 220", r.OrdersCount)
	}
	if r.MealType == nil || *r.MealType != "BREAKFAST" {
		t.Errorf("meal_type = %v, want BREAKFAST", r.MealType)
	}
}

// A .txt export that is really tab-separated must still import.
func TestParseTXTSniffsTabs(t *testing.T) {
	body := "date\tfootfall\torders\n2026-10-01\t120\t95\n"

	res := mustParse(t, "export.txt", body)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	if res.Rows[0].Footfall == nil || *res.Rows[0].Footfall != 120 {
		t.Errorf("footfall = %v, want 120", res.Rows[0].Footfall)
	}
}

func TestParseTSVExplicit(t *testing.T) {
	body := "date\tfootfall\n2026-10-01\t50\n"
	res := mustParse(t, "export.tsv", body)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
}

// One malformed line must not discard the whole file.
func TestParseSkipsBadRowsAndKeepsGood(t *testing.T) {
	csv := "date,footfall,orders\n" +
		"2026-10-01,100,90\n" +
		"not-a-date,110,95\n" +
		"2026-10-03,-5,80\n" +
		"2026-10-04,120,\n" +
		",,\n"

	res := mustParse(t, "mixed.csv", csv)
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2 (the two valid rows)", len(res.Rows))
	}
	if res.RowsRejected == 0 {
		t.Error("rejected rows should be reported")
	}
	if len(res.RejectSamples) == 0 {
		t.Error("rejection samples should be returned so the user can fix the file")
	}
}

// A footfall-only export is legitimate: the outcome columns may be absent.
func TestParseAcceptsFootfallOnly(t *testing.T) {
	csv := "date,attendance\n2026-10-01,300\n2026-10-02,310\n"
	res := mustParse(t, "minimal.csv", csv)
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	if res.Rows[0].FoodPrepared != nil {
		t.Error("prepared should stay nil when the column is absent")
	}
}

// Without a date column the row is still usable, stamped with today.
func TestParseImpliesDateWhenAbsent(t *testing.T) {
	csv := "footfall,orders\n300,250\n"
	res := mustParse(t, "nodate.csv", csv)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	if res.Rows[0].ObservedOn.IsZero() {
		t.Error("a date should be implied when the file omits one")
	}
}

func TestParseRejectsUnknownColumns(t *testing.T) {
	_, err := Parse("junk.csv", []byte("alpha,beta\n1,2\n"))
	if err == nil {
		t.Fatal("a file with no recognisable columns must be rejected")
	}
	if !strings.Contains(err.Error(), "no recognised columns") {
		t.Errorf("error = %v, want it to mention recognised columns", err)
	}
}

func TestParseRejectsUnsupportedExtension(t *testing.T) {
	_, err := Parse("data.pdf", []byte("%PDF-1.4"))
	if err == nil {
		t.Fatal("an unsupported file type must be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported file type") {
		t.Errorf("error = %v, want unsupported file type", err)
	}
}

func TestParseRejectsEmptyFile(t *testing.T) {
	if _, err := Parse("empty.csv", []byte("   \n\n")); err == nil {
		t.Fatal("an empty file must be rejected")
	}
}

func TestParseHandlesBOMAndCRLF(t *testing.T) {
	body := "\xEF\xBB\xBFdate,footfall\r\n2026-10-01,88\r\n"
	res := mustParse(t, "bom.csv", body)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	if res.Rows[0].Footfall == nil || *res.Rows[0].Footfall != 88 {
		t.Errorf("footfall = %v, want 88", res.Rows[0].Footfall)
	}
}

func TestParseSkipsLeadingBlankLines(t *testing.T) {
	body := "\n\ndate,footfall\n2026-10-01,77\n"
	res := mustParse(t, "lead.csv", body)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
}

// Day-of-week convention is 0 = Monday, matching the API contract.
func TestDerivedDayOfWeekMatchesContract(t *testing.T) {
	// 2026-10-05 is a Monday.
	body := "date,footfall\n2026-10-05,100\n"
	res := mustParse(t, "dow.csv", body)
	if res.Rows[0].DayOfWeek == nil || *res.Rows[0].DayOfWeek != 0 {
		t.Errorf("day_of_week = %v, want 0 for a Monday", res.Rows[0].DayOfWeek)
	}
}

func TestParseExcel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.xlsx")
	if err := writeTestWorkbook(t, path); err != nil {
		t.Skipf("could not build test workbook: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	res := mustParse(t, "history.xlsx", string(data))
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	if res.Rows[0].Footfall == nil || *res.Rows[0].Footfall != 210 {
		t.Errorf("footfall = %v, want 210", res.Rows[0].Footfall)
	}
	if res.Rows[0].OrdersCount == nil || *res.Rows[0].OrdersCount != 180 {
		t.Errorf("orders = %v, want 180", res.Rows[0].OrdersCount)
	}
}
