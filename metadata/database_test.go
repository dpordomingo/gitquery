package metadata

import (
	"testing"

	"github.com/gitql/gitql/sql"
	"github.com/stretchr/testify/assert"
)

func TestMetadataTables(t *testing.T) {
	metadataDB := NewDB(sql.NewCatalog())
	assert.Equal(t, SchemaDBname, metadataDB.Name())

	tables := metadataDB.Tables()
	assert.Contains(t, tables, SchemaDBTableName)
	assert.Contains(t, tables, SchemaTableTableName)
	assert.Contains(t, tables, SchemaColumnTableName)
	assert.Equal(t, 3, len(tables))
}
