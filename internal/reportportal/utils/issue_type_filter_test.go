package utils

import (
	"fmt"
	"testing"
)

func TestResolveDefectTypeToIssueTypeLocator(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"To Investigate", IssueLocatorToInvestigate},
		{"to investigate", IssueLocatorToInvestigate},
		{"TO_INVESTIGATE", IssueLocatorToInvestigate},
		{"To  Investigate", IssueLocatorToInvestigate},
		{"Product Bug", IssueLocatorProductBug},
		{"PRODUCT_BUG", IssueLocatorProductBug},
		{"Automation Bug", IssueLocatorAutomationBug},
		{"AUTOMATION_BUG", IssueLocatorAutomationBug},
		{"System Issue", IssueLocatorSystemIssue},
		{"SYSTEM_ISSUE", IssueLocatorSystemIssue},
		{"No Defect", IssueLocatorNoDefect},
		{"NO_DEFECT", IssueLocatorNoDefect},
		{"ti001", "ti001"},
		{"pb001", "pb001"},
		{"custom_nd002", "custom_nd002"},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("%d_%q", i, tt.in), func(t *testing.T) {
			got := ResolveDefectTypeToIssueTypeLocator(tt.in)
			if got != tt.want {
				t.Errorf("ResolveDefectTypeToIssueTypeLocator(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
