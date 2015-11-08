package main

import (
	"fmt"
	. "github.com/mpatraw/gocurse/curses"
	"strings"
)

type TableField struct {
	Header   string
	Width    int
	Format   string
	CellAttr int32

	fmt string
}

func (f *TableField) Align() {
	hLen := len(f.Header)
	if f.Format == "" {
		f.Format = fmt.Sprintf("v")
		f.Width = hLen
	}

	if f.Width >= 0 && hLen > f.Width {
		f.Width = hLen
	} else if f.Width < 0 && hLen > -f.Width {
		f.Width = -hLen
	}

	f.fmt = fmt.Sprintf("%%%d%s", f.Width, f.Format)
	if f.Width < 0 {
		f.Width = -f.Width
	}
	if f.Width > hLen {
		f.Header += strings.Repeat(" ", f.Width-hLen)
	}
}

func (f *TableField) Render(data interface{}) string {
	return fmt.Sprintf(f.fmt, data)
}

type TableRow struct {
	Num    int
	Values []interface{}
	Attrs  []int32
}

type Table struct {
	*WinPanel
	Fields []*TableField

	maxRows int
	rows    map[interface{}]*TableRow
}

func NewTable(height, width, y, x int, title string, fields []*TableField) *Table {
	t := &Table{
		WinPanel: NewWinPanel(height, width, y, x, true, "  "+title),
		Fields:   fields,

		maxRows: height - 3,
		rows:    make(map[interface{}]*TableRow),
	}

	col := 2
	for _, f := range t.Fields {
		f.Align()
		t.Addstr(col, 1, f.Header+"  ", A_UNDERLINE|A_BOLD)
		col += f.Width + 2
	}

	return t
}

func (t *Table) getRow(key interface{}) *TableRow {
	if _, ok := t.rows[key]; !ok {
		t.rows[key] = &TableRow{
			Num:   len(t.rows),
			Attrs: make([]int32, len(t.Fields)),
		}
		for n, f := range t.Fields {
			t.rows[key].Attrs[n] = f.CellAttr
		}
	}
	return t.rows[key]
}
func (t *Table) renderRow(row *TableRow) {
	col := 2
	for n, f := range t.Fields {
		t.Addstr(col, 2+row.Num, f.Render(row.Values[n]), row.Attrs[n])
		col += f.Width + 2
	}
}

func (t *Table) UpdateRowValues(key interface{}, values ...interface{}) {
	row := t.getRow(key)
	row.Values = values
	t.renderRow(row)
}

func (t *Table) SetRowColAttr(key interface{}, col int, attr int32) {
	row := t.getRow(key)
	row.Attrs[col] = attr
}

func (t *Table) DeleteRow(key interface{}) {
	n := t.rows[key].Num
	delete(t.rows, key)
	for _, row := range t.rows {
		if row.Num > n {
			row.Num--
			t.renderRow(row)
		}
	}
}
