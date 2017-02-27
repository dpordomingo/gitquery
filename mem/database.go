package mem

import "github.com/gitql/gitql/sql"

type Database struct {
	name   string
	tables map[string]sql.Table
}

func NewDatabase(name string) *Database {
	return &Database{
		name:   name,
		tables: map[string]sql.Table{},
	}
}

func (d *Database) Name() string {
	return d.name
}

func (d *Database) Tables() map[string]sql.Table {
	return d.tables
}

func (d *Database) AddTable(t sql.Table) {
	d.tables[t.Name()] = t
}
