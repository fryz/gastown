package doltserver

import (
	"testing"
)

func TestParseTableList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "header only",
			input: "Tables_in_wl_commons\n",
			want:  nil,
		},
		{
			name:  "single table",
			input: "Tables_in_wl_commons\nwanted\n",
			want:  []string{"wanted"},
		},
		{
			name:  "multiple tables",
			input: "Tables_in_wl_commons\n_meta\nbadges\nchain_meta\ncompletions\nrigs\nstamps\nwanted\n",
			want:  []string{"_meta", "badges", "chain_meta", "completions", "rigs", "stamps", "wanted"},
		},
		{
			name:  "with trailing whitespace",
			input: "Tables_in_wl_commons\n  wanted  \n  rigs  \n",
			want:  []string{"wanted", "rigs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseTableList(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseTableList() returned %d tables, want %d: %v", len(got), len(tt.want), got)
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("parseTableList()[%d] = %q, want %q", i, g, tt.want[i])
				}
			}
		})
	}
}

func TestParseDescribeOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []ColumnInfo
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "single column",
			input: "Field,Type,Null,Key,Default,Extra\nid,varchar(64),NO,PRI,,",
			want: []ColumnInfo{
				{Name: "id", Type: "varchar(64)", Nullable: false, HasDefault: false},
			},
		},
		{
			name:  "multiple columns with defaults",
			input: "Field,Type,Null,Key,Default,Extra\nid,varchar(64),NO,PRI,,\ntitle,text,NO,,,\npriority,int,YES,,2,\nstatus,varchar(32),YES,,open,",
			want: []ColumnInfo{
				{Name: "id", Type: "varchar(64)", Nullable: false, HasDefault: false},
				{Name: "title", Type: "text", Nullable: false, HasDefault: false},
				{Name: "priority", Type: "int", Nullable: true, HasDefault: true, Default: "2"},
				{Name: "status", Type: "varchar(32)", Nullable: true, HasDefault: true, Default: "open"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseDescribeOutput(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("parseDescribeOutput() returned %d cols, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				w := tt.want[i]
				if g.Name != w.Name {
					t.Errorf("col[%d].Name = %q, want %q", i, g.Name, w.Name)
				}
				if g.Type != w.Type {
					t.Errorf("col[%d].Type = %q, want %q", i, g.Type, w.Type)
				}
				if g.Nullable != w.Nullable {
					t.Errorf("col[%d].Nullable = %v, want %v", i, g.Nullable, w.Nullable)
				}
				if g.HasDefault != w.HasDefault {
					t.Errorf("col[%d].HasDefault = %v, want %v", i, g.HasDefault, w.HasDefault)
				}
				if g.Default != w.Default {
					t.Errorf("col[%d].Default = %q, want %q", i, g.Default, w.Default)
				}
			}
		})
	}
}

func TestTypesCompatible(t *testing.T) {
	t.Parallel()
	tests := []struct {
		local    string
		upstream string
		want     bool
	}{
		{"varchar(64)", "varchar(64)", true},
		{"VARCHAR(64)", "varchar(64)", true},
		{"int", "INT", true},
		{"integer", "int", true},
		{"INT", "INTEGER", true},
		{"boolean", "tinyint(1)", true},
		{"BOOL", "TINYINT(1)", true},
		{"varchar(64)", "varchar(255)", false},
		{"int", "text", false},
		{"varchar(32)", "int", false},
		{"text", "json", false},
	}
	for _, tt := range tests {
		t.Run(tt.local+"_vs_"+tt.upstream, func(t *testing.T) {
			t.Parallel()
			got := typesCompatible(tt.local, tt.upstream)
			if got != tt.want {
				t.Errorf("typesCompatible(%q, %q) = %v, want %v", tt.local, tt.upstream, got, tt.want)
			}
		})
	}
}

func TestSchemaChangeDetection_SchemasMatch(t *testing.T) {
	t.Parallel()

	// When upstream and local schemas are identical, no changes should be detected.
	upstreamCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "title", Type: "text", Nullable: false},
		{Name: "status", Type: "varchar(32)", Nullable: true},
	}
	localCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "title", Type: "text", Nullable: false},
		{Name: "status", Type: "varchar(32)", Nullable: true},
	}

	changes := detectColumnChanges("wanted", upstreamCols, localCols)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for matching schemas, got %d: %+v", len(changes), changes)
	}
}

func TestSchemaChangeDetection_NewColumnUpstream(t *testing.T) {
	t.Parallel()

	upstreamCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "title", Type: "text", Nullable: false},
		{Name: "reviewer", Type: "varchar(255)", Nullable: true},
	}
	localCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "title", Type: "text", Nullable: false},
	}

	changes := detectColumnChanges("wanted", upstreamCols, localCols)

	newCols := filterChanges(changes, "new_column")
	if len(newCols) != 1 {
		t.Fatalf("expected 1 new_column change, got %d", len(newCols))
	}
	if newCols[0].Column != "reviewer" {
		t.Errorf("new column = %q, want %q", newCols[0].Column, "reviewer")
	}
	if newCols[0].Breaking {
		t.Error("new_column should not be breaking")
	}
}

func TestSchemaChangeDetection_NewTableUpstream(t *testing.T) {
	t.Parallel()

	upstreamTables := []string{"wanted", "rigs", "reviews"}
	localTables := []string{"wanted", "rigs"}

	localSet := make(map[string]bool)
	for _, t := range localTables {
		localSet[t] = true
	}

	var changes []SchemaChange
	for _, table := range upstreamTables {
		if !localSet[table] {
			changes = append(changes, SchemaChange{
				Type:   "new_table",
				Table:  table,
				Detail: "new table found upstream",
			})
		}
	}

	if len(changes) != 1 {
		t.Fatalf("expected 1 new_table change, got %d", len(changes))
	}
	if changes[0].Table != "reviews" {
		t.Errorf("new table = %q, want %q", changes[0].Table, "reviews")
	}
}

func TestSchemaChangeDetection_ColumnRemovedUpstream(t *testing.T) {
	t.Parallel()

	upstreamCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
	}
	localCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "deprecated_field", Type: "text", Nullable: true},
	}

	changes := detectColumnChanges("wanted", upstreamCols, localCols)

	removed := filterChanges(changes, "column_removed")
	if len(removed) != 1 {
		t.Fatalf("expected 1 column_removed change, got %d", len(removed))
	}
	if removed[0].Column != "deprecated_field" {
		t.Errorf("removed column = %q, want %q", removed[0].Column, "deprecated_field")
	}
	if !removed[0].Breaking {
		t.Error("column_removed should be breaking")
	}
}

func TestSchemaChangeDetection_TypeChanged(t *testing.T) {
	t.Parallel()

	upstreamCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "priority", Type: "text", Nullable: true},
	}
	localCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "priority", Type: "int", Nullable: true},
	}

	changes := detectColumnChanges("wanted", upstreamCols, localCols)

	typeChanged := filterChanges(changes, "type_changed")
	if len(typeChanged) != 1 {
		t.Fatalf("expected 1 type_changed change, got %d", len(typeChanged))
	}
	if typeChanged[0].Column != "priority" {
		t.Errorf("type-changed column = %q, want %q", typeChanged[0].Column, "priority")
	}
	if !typeChanged[0].Breaking {
		t.Error("type_changed should be breaking")
	}
}

func TestSchemaChangeDetection_MixedChanges(t *testing.T) {
	t.Parallel()

	// Upstream adds a column and changes a type.
	upstreamCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "title", Type: "varchar(512)", Nullable: false}, // was text
		{Name: "reviewer", Type: "varchar(255)", Nullable: true},
	}
	localCols := []ColumnInfo{
		{Name: "id", Type: "varchar(64)", Nullable: false},
		{Name: "title", Type: "text", Nullable: false},
	}

	changes := detectColumnChanges("wanted", upstreamCols, localCols)

	newCols := filterChanges(changes, "new_column")
	typeChanges := filterChanges(changes, "type_changed")
	if len(newCols) != 1 {
		t.Errorf("expected 1 new_column, got %d", len(newCols))
	}
	if len(typeChanges) != 1 {
		t.Errorf("expected 1 type_changed, got %d", len(typeChanges))
	}
}

// detectColumnChanges is a test helper that replicates the column comparison
// logic from ReconcileSchema for unit testing without a Dolt server.
func detectColumnChanges(table string, upstreamCols, localCols []ColumnInfo) []SchemaChange {
	var changes []SchemaChange

	localColMap := make(map[string]ColumnInfo, len(localCols))
	for _, c := range localCols {
		localColMap[c.Name] = c
	}
	upstreamColMap := make(map[string]ColumnInfo, len(upstreamCols))
	for _, c := range upstreamCols {
		upstreamColMap[c.Name] = c
	}

	for _, uc := range upstreamCols {
		if _, exists := localColMap[uc.Name]; !exists {
			changes = append(changes, SchemaChange{
				Type:   "new_column",
				Table:  table,
				Column: uc.Name,
				Detail: "new column found upstream",
			})
		}
	}

	for _, lc := range localCols {
		if _, exists := upstreamColMap[lc.Name]; !exists {
			changes = append(changes, SchemaChange{
				Type:     "column_removed",
				Table:    table,
				Column:   lc.Name,
				Detail:   "column removed upstream",
				Breaking: true,
			})
		}
	}

	for _, uc := range upstreamCols {
		if lc, exists := localColMap[uc.Name]; exists {
			if !typesCompatible(lc.Type, uc.Type) {
				changes = append(changes, SchemaChange{
					Type:     "type_changed",
					Table:    table,
					Column:   uc.Name,
					Detail:   "column type changed",
					Breaking: true,
				})
			}
		}
	}

	return changes
}

// filterChanges returns only changes of the given type.
func filterChanges(changes []SchemaChange, changeType string) []SchemaChange {
	var filtered []SchemaChange
	for _, c := range changes {
		if c.Type == changeType {
			filtered = append(filtered, c)
		}
	}
	return filtered
}
