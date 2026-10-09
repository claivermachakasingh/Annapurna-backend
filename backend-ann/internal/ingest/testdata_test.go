package ingest

import (
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeTestWorkbook creates a minimal two-row workbook for the Excel test.
func writeTestWorkbook(t *testing.T, path string) error {
	t.Helper()

	f := excelize.NewFile()
	defer f.Close()

	sheet := f.GetSheetList()[0]
	rows := [][]interface{}{
		{"Date", "Footfall", "Orders"},
		{"2026-10-01", 210, 180},
		{"2026-10-02", 240, 205},
	}
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			return err
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			return err
		}
	}
	return f.SaveAs(path)
}
