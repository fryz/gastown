// Package doltserver - schema_evolution.go provides schema reconciliation for wl-commons.
//
// When gt wl sync pulls upstream changes that include schema modifications
// (new columns, new tables, type changes), ReconcileSchema detects and
// auto-applies backward-compatible changes while warning about breaking ones.
package doltserver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SchemaChange describes a single schema difference detected during reconciliation.
type SchemaChange struct {
	// Type is one of: "new_column", "new_table", "column_removed", "type_changed".
	Type string

	// Table is the affected table name.
	Table string

	// Column is the affected column name (empty for new_table).
	Column string

	// Detail provides human-readable context about the change.
	Detail string

	// Breaking is true if this change cannot be auto-applied.
	Breaking bool
}

// ReconcileSchemaResult holds the outcome of a schema reconciliation.
type ReconcileSchemaResult struct {
	// Changes lists all detected schema differences.
	Changes []SchemaChange

	// Applied counts how many changes were successfully auto-applied.
	Applied int

	// Warnings lists human-readable messages for breaking changes that were skipped.
	Warnings []string
}

// ColumnInfo describes a column in a table schema.
type ColumnInfo struct {
	Name       string
	Type       string
	Nullable   bool
	Default    string
	HasDefault bool
}

// ReconcileSchema compares the upstream schema (in forkDir) with the local
// Dolt server's wl_commons database and applies backward-compatible changes.
//
// New columns are added with ALTER TABLE ADD COLUMN. New tables are created
// with CREATE TABLE IF NOT EXISTS. Breaking changes (column removals, type
// changes) produce warnings but do not crash or modify data.
//
// The schema_version in _meta is updated after successful reconciliation.
func ReconcileSchema(townRoot, forkDir string) (*ReconcileSchemaResult, error) {
	result := &ReconcileSchemaResult{}

	// Get upstream table list from the fork directory.
	upstreamTables, err := listTablesFromFork(forkDir)
	if err != nil {
		return nil, fmt.Errorf("listing upstream tables: %w", err)
	}

	// Get local table list from the Dolt server.
	localTables, err := listTablesFromServer(townRoot)
	if err != nil {
		return nil, fmt.Errorf("listing local tables: %w", err)
	}

	localTableSet := make(map[string]bool, len(localTables))
	for _, t := range localTables {
		localTableSet[t] = true
	}

	var alterStatements []string

	// Check for new tables upstream that don't exist locally.
	for _, table := range upstreamTables {
		if !localTableSet[table] {
			change := SchemaChange{
				Type:   "new_table",
				Table:  table,
				Detail: fmt.Sprintf("new table %q found upstream", table),
			}
			result.Changes = append(result.Changes, change)

			createSQL, err := getCreateTableFromFork(forkDir, table)
			if err != nil {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("could not get CREATE TABLE for %q: %v", table, err))
				continue
			}
			alterStatements = append(alterStatements, createSQL)
		}
	}

	// For tables that exist in both, compare columns.
	for _, table := range upstreamTables {
		if !localTableSet[table] {
			continue
		}

		upstreamCols, err := describeTableFromFork(forkDir, table)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("could not describe upstream table %q: %v", table, err))
			continue
		}

		localCols, err := describeTableFromServer(townRoot, table)
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("could not describe local table %q: %v", table, err))
			continue
		}

		localColMap := make(map[string]ColumnInfo, len(localCols))
		for _, c := range localCols {
			localColMap[c.Name] = c
		}
		upstreamColMap := make(map[string]ColumnInfo, len(upstreamCols))
		for _, c := range upstreamCols {
			upstreamColMap[c.Name] = c
		}

		// Detect new columns upstream.
		for _, uc := range upstreamCols {
			if _, exists := localColMap[uc.Name]; !exists {
				change := SchemaChange{
					Type:   "new_column",
					Table:  table,
					Column: uc.Name,
					Detail: fmt.Sprintf("new column %q (%s) in table %q", uc.Name, uc.Type, table),
				}
				result.Changes = append(result.Changes, change)

				alterSQL := fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` %s", table, uc.Name, uc.Type)
				if uc.HasDefault {
					alterSQL += fmt.Sprintf(" DEFAULT %s", uc.Default)
				} else if uc.Nullable {
					alterSQL += " DEFAULT NULL"
				}
				alterStatements = append(alterStatements, alterSQL)
			}
		}

		// Detect columns removed upstream (breaking).
		for _, lc := range localCols {
			if _, exists := upstreamColMap[lc.Name]; !exists {
				change := SchemaChange{
					Type:     "column_removed",
					Table:    table,
					Column:   lc.Name,
					Detail:   fmt.Sprintf("column %q removed from table %q upstream", lc.Name, table),
					Breaking: true,
				}
				result.Changes = append(result.Changes, change)
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("breaking: column %q was removed from %q upstream (not auto-applied)", lc.Name, table))
			}
		}

		// Detect type changes (breaking).
		for _, uc := range upstreamCols {
			if lc, exists := localColMap[uc.Name]; exists {
				if !typesCompatible(lc.Type, uc.Type) {
					change := SchemaChange{
						Type:     "type_changed",
						Table:    table,
						Column:   uc.Name,
						Detail:   fmt.Sprintf("column %q in %q changed type: %s -> %s", uc.Name, table, lc.Type, uc.Type),
						Breaking: true,
					}
					result.Changes = append(result.Changes, change)
					result.Warnings = append(result.Warnings,
						fmt.Sprintf("breaking: column %q in %q type changed from %s to %s (not auto-applied)", uc.Name, table, lc.Type, uc.Type))
				}
			}
		}
	}

	// Apply backward-compatible changes.
	if len(alterStatements) > 0 {
		script := fmt.Sprintf("USE %s;\n", WLCommonsDB)
		for _, stmt := range alterStatements {
			script += stmt + ";\n"
		}
		script += "CALL DOLT_ADD('-A');\n"
		script += "CALL DOLT_COMMIT('--allow-empty', '-m', 'schema evolution: reconcile with upstream');\n"

		if err := doltSQLScriptWithRetry(townRoot, script); err != nil {
			if !isNothingToCommit(err) {
				return result, fmt.Errorf("applying schema changes: %w", err)
			}
		}

		for _, c := range result.Changes {
			if !c.Breaking {
				result.Applied++
			}
		}
	}

	// Update schema_version in _meta if changes were applied.
	if result.Applied > 0 {
		version := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		updateMeta := fmt.Sprintf(`USE %s;
REPLACE INTO _meta (%s, value) VALUES ('schema_version', '%s');
REPLACE INTO _meta (%s, value) VALUES ('schema_reconciled_at', '%s');
CALL DOLT_ADD('-A');
CALL DOLT_COMMIT('--allow-empty', '-m', 'update schema_version after reconciliation');
`, WLCommonsDB, backtickKey(), version, backtickKey(), version)

		if err := doltSQLScriptWithRetry(townRoot, updateMeta); err != nil {
			if !isNothingToCommit(err) {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("failed to update _meta schema_version: %v", err))
			}
		}
	}

	return result, nil
}

// listTablesFromFork returns table names from the fork directory using dolt sql.
func listTablesFromFork(forkDir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dolt", "sql", "-q", "SHOW TABLES", "-r", "csv")
	cmd.Dir = forkDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("dolt sql SHOW TABLES: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return parseTableList(string(output)), nil
}

// listTablesFromServer returns table names from the Dolt server's wl_commons database.
func listTablesFromServer(townRoot string) ([]string, error) {
	query := fmt.Sprintf("USE %s; SHOW TABLES", WLCommonsDB)
	output, err := doltSQLQuery(townRoot, query)
	if err != nil {
		return nil, err
	}
	return parseTableList(output), nil
}

// parseTableList parses CSV output from SHOW TABLES into a slice of table names.
func parseTableList(csvOutput string) []string {
	lines := strings.Split(strings.TrimSpace(csvOutput), "\n")
	var tables []string
	for i, line := range lines {
		if i == 0 { // skip header
			continue
		}
		name := strings.TrimSpace(line)
		if name != "" {
			tables = append(tables, name)
		}
	}
	return tables
}

// getCreateTableFromFork returns the CREATE TABLE statement for a table in the fork.
func getCreateTableFromFork(forkDir, table string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	query := fmt.Sprintf("SHOW CREATE TABLE `%s`", table)
	cmd := exec.CommandContext(ctx, "dolt", "sql", "-q", query, "-r", "csv")
	cmd.Dir = forkDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("SHOW CREATE TABLE %s: %w (%s)", table, err, strings.TrimSpace(string(output)))
	}

	rows := parseSimpleCSV(string(output))
	if len(rows) == 0 {
		return "", fmt.Errorf("no output from SHOW CREATE TABLE %s", table)
	}

	for _, v := range rows[0] {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(v)), "CREATE TABLE") {
			return v, nil
		}
	}

	return "", fmt.Errorf("CREATE TABLE statement not found in output for %s", table)
}

// describeTableFromFork returns column info for a table in the fork directory.
func describeTableFromFork(forkDir, table string) ([]ColumnInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	query := fmt.Sprintf("DESCRIBE `%s`", table)
	cmd := exec.CommandContext(ctx, "dolt", "sql", "-q", query, "-r", "csv")
	cmd.Dir = forkDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("DESCRIBE %s: %w (%s)", table, err, strings.TrimSpace(string(output)))
	}
	return parseDescribeOutput(string(output)), nil
}

// describeTableFromServer returns column info for a table in the Dolt server's wl_commons.
func describeTableFromServer(townRoot, table string) ([]ColumnInfo, error) {
	query := fmt.Sprintf("USE %s; DESCRIBE `%s`", WLCommonsDB, table)
	output, err := doltSQLQuery(townRoot, query)
	if err != nil {
		return nil, err
	}
	return parseDescribeOutput(output), nil
}

// parseDescribeOutput parses CSV output from DESCRIBE TABLE into ColumnInfo slices.
// DESCRIBE output columns: Field, Type, Null, Key, Default, Extra
func parseDescribeOutput(csvOutput string) []ColumnInfo {
	rows := parseSimpleCSV(csvOutput)
	var cols []ColumnInfo
	for _, row := range rows {
		col := ColumnInfo{
			Name:     row["Field"],
			Type:     row["Type"],
			Nullable: strings.ToUpper(row["Null"]) == "YES",
		}
		if def, ok := row["Default"]; ok && def != "" {
			col.HasDefault = true
			col.Default = def
		}
		cols = append(cols, col)
	}
	return cols
}

// typesCompatible returns true if two SQL type strings are considered compatible
// (i.e., the change does not require data migration). This is a conservative check
// that normalizes common type aliases.
func typesCompatible(localType, upstreamType string) bool {
	normalize := func(t string) string {
		t = strings.ToUpper(strings.TrimSpace(t))
		t = strings.ReplaceAll(t, "INTEGER", "INT")
		t = strings.ReplaceAll(t, "BOOLEAN", "TINYINT(1)")
		t = strings.ReplaceAll(t, "BOOL", "TINYINT(1)")
		return t
	}
	return normalize(localType) == normalize(upstreamType)
}
