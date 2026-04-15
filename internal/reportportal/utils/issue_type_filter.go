package utils

import "strings"

// Default issue locators for the built-in ReportPortal defect groups. Custom projects may define
// additional subtypes with different locators; those should be passed verbatim (see get_project_defect_types).
const (
	IssueLocatorToInvestigate  = "ti001"
	IssueLocatorProductBug     = "pb001"
	IssueLocatorAutomationBug  = "ab001"
	IssueLocatorSystemIssue    = "si001"
	IssueLocatorNoDefect       = "nd001"
)

// ResolveDefectTypeToIssueTypeLocator converts a human-readable defect name, a type reference
// (e.g. TO_INVESTIGATE), or collapses spacing and matches case-insensitively. Any other non-empty
// string is returned unchanged so callers can pass a project-specific locator from get_project_defect_types.
// Empty input returns empty output.
func ResolveDefectTypeToIssueTypeLocator(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}

	ref := strings.ToUpper(strings.Join(strings.Fields(trimmed), "_"))
	switch ref {
	case "TO_INVESTIGATE":
		return IssueLocatorToInvestigate
	case "PRODUCT_BUG":
		return IssueLocatorProductBug
	case "AUTOMATION_BUG":
		return IssueLocatorAutomationBug
	case "SYSTEM_ISSUE":
		return IssueLocatorSystemIssue
	case "NO_DEFECT":
		return IssueLocatorNoDefect
	}

	switch strings.ToLower(strings.Join(strings.Fields(trimmed), " ")) {
	case "to investigate":
		return IssueLocatorToInvestigate
	case "product bug":
		return IssueLocatorProductBug
	case "automation bug":
		return IssueLocatorAutomationBug
	case "system issue":
		return IssueLocatorSystemIssue
	case "no defect":
		return IssueLocatorNoDefect
	}

	return trimmed
}
