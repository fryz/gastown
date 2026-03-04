// Package doltserver - stamp_validation.go implements the stamp validation pipeline
// for the Wasteland federation. Stamps are attestations that validate completed work.
// This pipeline checks stamps for correctness, consistency, and fraud patterns.
package doltserver

import (
	"fmt"
	"time"
)

// StampRejection records a stamp that failed validation.
type StampRejection struct {
	StampID string `json:"stamp_id"`
	Rule    string `json:"rule"`
	Reason  string `json:"reason"`
}

// StampWarning records a non-fatal validation issue.
type StampWarning struct {
	StampID string `json:"stamp_id"`
	Rule    string `json:"rule"`
	Reason  string `json:"reason"`
}

// FraudAlert records a suspicious pattern detected across stamps.
type FraudAlert struct {
	Pattern     string   `json:"pattern"`
	Description string   `json:"description"`
	RigHandles  []string `json:"rig_handles"`
}

// StampValidationResult contains the full results of stamp validation.
type StampValidationResult struct {
	TotalStamps    int              `json:"total_stamps"`
	ValidStamps    int              `json:"valid_stamps"`
	RejectedStamps []StampRejection `json:"rejected_stamps"`
	Warnings       []StampWarning   `json:"warnings"`
	FraudAlerts    []FraudAlert     `json:"fraud_alerts"`
	RunAt          time.Time        `json:"run_at"`
}

// ValidateStamps runs the full stamp validation pipeline against the wl-commons
// database. It checks all stamps for correctness rules and fraud patterns.
func ValidateStamps(townRoot string) (*StampValidationResult, error) {
	result := &StampValidationResult{
		RunAt: time.Now().UTC(),
	}

	// Count total stamps
	count, err := queryStampCount(townRoot)
	if err != nil {
		return nil, fmt.Errorf("counting stamps: %w", err)
	}
	result.TotalStamps = count

	// Rule 1: Author must exist in rigs table
	rejections, err := checkUnregisteredAuthors(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking unregistered authors: %w", err)
	}
	result.RejectedStamps = append(result.RejectedStamps, rejections...)

	// Rule 2: Subject must exist in rigs table
	rejections, err = checkUnregisteredSubjects(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking unregistered subjects: %w", err)
	}
	result.RejectedStamps = append(result.RejectedStamps, rejections...)

	// Rule 3: No self-stamping
	rejections, err = checkSelfStamping(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking self-stamping: %w", err)
	}
	result.RejectedStamps = append(result.RejectedStamps, rejections...)

	// Rule 4: Author must have trust_level >= 2
	rejections, err = checkInsufficientTrust(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking trust levels: %w", err)
	}
	result.RejectedStamps = append(result.RejectedStamps, rejections...)

	// Rule 5: Referenced completion must exist
	rejections, err = checkMissingCompletions(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking missing completions: %w", err)
	}
	result.RejectedStamps = append(result.RejectedStamps, rejections...)

	// Rule 6: No duplicate stamps (warning)
	warnings, err := checkDuplicateStamps(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking duplicate stamps: %w", err)
	}
	result.Warnings = append(result.Warnings, warnings...)

	// Rule 7: Hash chain integrity (warning)
	warnings, err = checkBrokenHashChain(townRoot)
	if err != nil {
		return nil, fmt.Errorf("checking hash chain: %w", err)
	}
	result.Warnings = append(result.Warnings, warnings...)

	// Fraud detection
	alerts, err := detectStampRings(townRoot)
	if err != nil {
		return nil, fmt.Errorf("detecting stamp rings: %w", err)
	}
	result.FraudAlerts = append(result.FraudAlerts, alerts...)

	alerts, err = detectRubberStamping(townRoot)
	if err != nil {
		return nil, fmt.Errorf("detecting rubber-stamping: %w", err)
	}
	result.FraudAlerts = append(result.FraudAlerts, alerts...)

	alerts, err = detectSingleSourceDependency(townRoot)
	if err != nil {
		return nil, fmt.Errorf("detecting single-source dependency: %w", err)
	}
	result.FraudAlerts = append(result.FraudAlerts, alerts...)

	// Calculate valid stamps (dedup rejected IDs)
	rejectedIDs := make(map[string]bool)
	for _, r := range result.RejectedStamps {
		rejectedIDs[r.StampID] = true
	}
	result.ValidStamps = result.TotalStamps - len(rejectedIDs)

	return result, nil
}

func queryStampCount(townRoot string) (int, error) {
	output, err := doltSQLQuery(townRoot, fmt.Sprintf("USE %s; SELECT COUNT(*) as cnt FROM stamps", WLCommonsDB))
	if err != nil {
		return 0, err
	}
	rows := parseSimpleCSV(output)
	if len(rows) == 0 {
		return 0, nil
	}
	count := 0
	if v, ok := rows[0]["cnt"]; ok {
		fmt.Sscanf(v, "%d", &count)
	}
	return count, nil
}

func wlValidationQuery(townRoot, query string) (string, error) {
	return doltSQLQuery(townRoot, fmt.Sprintf("USE %s; %s", WLCommonsDB, query))
}

func checkUnregisteredAuthors(townRoot string) ([]StampRejection, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT s.id, s.author FROM stamps s LEFT JOIN rigs r ON s.author = r.handle WHERE r.handle IS NULL`)
	if err != nil {
		return nil, err
	}
	var rejections []StampRejection
	for _, row := range parseSimpleCSV(output) {
		rejections = append(rejections, StampRejection{
			StampID: row["id"],
			Rule:    "author_exists",
			Reason:  fmt.Sprintf("author %q is not a registered rig", row["author"]),
		})
	}
	return rejections, nil
}

func checkUnregisteredSubjects(townRoot string) ([]StampRejection, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT s.id, s.subject FROM stamps s LEFT JOIN rigs r ON s.subject = r.handle WHERE r.handle IS NULL`)
	if err != nil {
		return nil, err
	}
	var rejections []StampRejection
	for _, row := range parseSimpleCSV(output) {
		rejections = append(rejections, StampRejection{
			StampID: row["id"],
			Rule:    "subject_exists",
			Reason:  fmt.Sprintf("subject %q is not a registered rig", row["subject"]),
		})
	}
	return rejections, nil
}

func checkSelfStamping(townRoot string) ([]StampRejection, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT id, author, subject FROM stamps WHERE author = subject`)
	if err != nil {
		return nil, err
	}
	var rejections []StampRejection
	for _, row := range parseSimpleCSV(output) {
		rejections = append(rejections, StampRejection{
			StampID: row["id"],
			Rule:    "no_self_stamp",
			Reason:  fmt.Sprintf("rig %q cannot stamp itself", row["author"]),
		})
	}
	return rejections, nil
}

func checkInsufficientTrust(townRoot string) ([]StampRejection, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT s.id, s.author, r.trust_level FROM stamps s JOIN rigs r ON s.author = r.handle WHERE r.trust_level < 2`)
	if err != nil {
		return nil, err
	}
	var rejections []StampRejection
	for _, row := range parseSimpleCSV(output) {
		rejections = append(rejections, StampRejection{
			StampID: row["id"],
			Rule:    "author_trust_level",
			Reason:  fmt.Sprintf("author %q has trust_level %s (minimum 2 required)", row["author"], row["trust_level"]),
		})
	}
	return rejections, nil
}

func checkMissingCompletions(townRoot string) ([]StampRejection, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT s.id, s.context_id FROM stamps s LEFT JOIN completions c ON s.context_id = c.id WHERE s.context_type = 'completion' AND c.id IS NULL`)
	if err != nil {
		return nil, err
	}
	var rejections []StampRejection
	for _, row := range parseSimpleCSV(output) {
		rejections = append(rejections, StampRejection{
			StampID: row["id"],
			Rule:    "completion_exists",
			Reason:  fmt.Sprintf("referenced completion %q does not exist", row["context_id"]),
		})
	}
	return rejections, nil
}

func checkDuplicateStamps(townRoot string) ([]StampWarning, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT author, subject, context_id, COUNT(*) as cnt FROM stamps WHERE context_id IS NOT NULL GROUP BY author, subject, context_id HAVING cnt > 1`)
	if err != nil {
		return nil, err
	}
	var warnings []StampWarning
	for _, row := range parseSimpleCSV(output) {
		warnings = append(warnings, StampWarning{
			StampID: fmt.Sprintf("%s->%s:%s", row["author"], row["subject"], row["context_id"]),
			Rule:    "no_duplicates",
			Reason:  fmt.Sprintf("author %q stamped subject %q for context %q %s times", row["author"], row["subject"], row["context_id"], row["cnt"]),
		})
	}
	return warnings, nil
}

func checkBrokenHashChain(townRoot string) ([]StampWarning, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT s.id, s.prev_stamp_hash FROM stamps s WHERE s.prev_stamp_hash IS NOT NULL AND NOT EXISTS (SELECT 1 FROM stamps s2 WHERE s2.block_hash = s.prev_stamp_hash)`)
	if err != nil {
		return nil, err
	}
	var warnings []StampWarning
	for _, row := range parseSimpleCSV(output) {
		warnings = append(warnings, StampWarning{
			StampID: row["id"],
			Rule:    "hash_chain_integrity",
			Reason:  fmt.Sprintf("prev_stamp_hash %q does not reference any existing stamp", row["prev_stamp_hash"]),
		})
	}
	return warnings, nil
}

func detectStampRings(townRoot string) ([]FraudAlert, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT s1.author as rig_a, s1.subject as rig_b, COUNT(*) as cnt FROM stamps s1 JOIN stamps s2 ON s1.author = s2.subject AND s1.subject = s2.author GROUP BY rig_a, rig_b HAVING cnt > 1`)
	if err != nil {
		return nil, err
	}
	var alerts []FraudAlert
	for _, row := range parseSimpleCSV(output) {
		alerts = append(alerts, FraudAlert{
			Pattern:     "stamp_ring",
			Description: fmt.Sprintf("mutual stamping ring: %s and %s stamped each other %s times", row["rig_a"], row["rig_b"], row["cnt"]),
			RigHandles:  []string{row["rig_a"], row["rig_b"]},
		})
	}
	return alerts, nil
}

func detectRubberStamping(townRoot string) ([]FraudAlert, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT author, COUNT(*) as stamp_count, TIMESTAMPDIFF(MINUTE, MIN(created_at), MAX(created_at)) as window_minutes FROM stamps GROUP BY author HAVING stamp_count > 5 AND window_minutes < stamp_count * 5`)
	if err != nil {
		return nil, err
	}
	var alerts []FraudAlert
	for _, row := range parseSimpleCSV(output) {
		alerts = append(alerts, FraudAlert{
			Pattern:     "rubber_stamping",
			Description: fmt.Sprintf("rig %q issued %s stamps in %s minutes (suspiciously fast)", row["author"], row["stamp_count"], row["window_minutes"]),
			RigHandles:  []string{row["author"]},
		})
	}
	return alerts, nil
}

func detectSingleSourceDependency(townRoot string) ([]FraudAlert, error) {
	output, err := wlValidationQuery(townRoot,
		`SELECT subject, COUNT(DISTINCT author) as unique_stampers, COUNT(*) as total_stamps FROM stamps GROUP BY subject HAVING total_stamps > 2 AND unique_stampers = 1`)
	if err != nil {
		return nil, err
	}
	var alerts []FraudAlert
	for _, row := range parseSimpleCSV(output) {
		alerts = append(alerts, FraudAlert{
			Pattern:     "single_source_dependency",
			Description: fmt.Sprintf("subject %q has %s stamps all from a single author", row["subject"], row["total_stamps"]),
			RigHandles:  []string{row["subject"]},
		})
	}
	return alerts, nil
}
