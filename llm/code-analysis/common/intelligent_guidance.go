package common

import (
	"fmt"
	"strings"
)

// IntelligentGuidanceGenerator creates context-aware guidance based on problem analysis
type IntelligentGuidanceGenerator struct{}

// GenerateGuidanceFromProblem analyzes the problem and generates appropriate guidance
func (ig *IntelligentGuidanceGenerator) GenerateGuidanceFromProblem(problemDescription string, hasRepository bool, repositoryCloned bool) string {
	analysis := ig.analyzeProblemType(problemDescription)

	guidance := "## INTELLIGENT PROBLEM ANALYSIS\n\n"

	// Add problem-specific guidance
	guidance += fmt.Sprintf("**Problem Type:** %s\n", analysis.ProblemType)
	guidance += fmt.Sprintf("**Complexity:** %s\n", analysis.Complexity)
	guidance += fmt.Sprintf("**Investigation Strategy:** %s\n\n", analysis.Strategy)

	// Add repository guidance based on needs
	if analysis.RequiresCodeAccess {
		if !repositoryCloned && hasRepository {
			guidance += "**CRITICAL FIRST STEP:** Clone repository to access code files\n"
			guidance += "**Tool to Use:** `repo_clone` - This gives you access to the actual codebase\n\n"
		} else if repositoryCloned {
			guidance += "**Code Access:** Available - proceed with file analysis\n\n"
		}
	}

	// Add tool recommendations
	guidance += "**Recommended Tool Sequence:**\n"
	for i, tool := range analysis.ToolSequence {
		guidance += fmt.Sprintf("%d. %s - %s\n", i+1, tool.Name, tool.Purpose)
	}

	return guidance
}

// ProblemAnalysis contains the analysis results
type ProblemAnalysis struct {
	ProblemType        string
	Complexity         string
	Strategy           string
	RequiresCodeAccess bool
	ToolSequence       []ToolRecommendation
}

// ToolRecommendation suggests tools for the problem
type ToolRecommendation struct {
	Name    string
	Purpose string
}

// analyzeProblemType determines what kind of problem we're dealing with
func (ig *IntelligentGuidanceGenerator) analyzeProblemType(problemDescription string) ProblemAnalysis {
	description := strings.ToLower(problemDescription)

	// Analyze problem indicators
	if strings.Contains(description, "validation error") || strings.Contains(description, "pydantic") {
		return ProblemAnalysis{
			ProblemType:        "Data Validation Error",
			Complexity:         "Medium",
			Strategy:           "Find the model definition and trace data flow to identify missing fields",
			RequiresCodeAccess: true,
			ToolSequence: []ToolRecommendation{
				{"repo_clone", "Access the codebase"},
				{"file_find", "Locate the relevant model files"},
				{"file_view", "Examine model definitions and validation logic"},
				{"grep", "Search for field usage patterns"},
			},
		}
	} else if strings.Contains(description, "performance") || strings.Contains(description, "slow") {
		return ProblemAnalysis{
			ProblemType:        "Performance Issue",
			Complexity:         "High",
			Strategy:           "Profile code paths and identify bottlenecks through analysis",
			RequiresCodeAccess: true,
			ToolSequence: []ToolRecommendation{
				{"repo_clone", "Access the codebase"},
				{"grep", "Search for performance-critical code patterns"},
				{"git", "Analyze recent changes that might affect performance"},
			},
		}
	} else if strings.Contains(description, "security") || strings.Contains(description, "cve") {
		return ProblemAnalysis{
			ProblemType:        "Security Vulnerability",
			Complexity:         "High",
			Strategy:           "Audit dependencies and code for security issues",
			RequiresCodeAccess: true,
			ToolSequence: []ToolRecommendation{
				{"repo_clone", "Access the codebase"},
				{"grep", "Search for vulnerable patterns"},
				{"file_view", "Examine security-sensitive files"},
			},
		}
	}

	// Default analysis for unknown problems
	return ProblemAnalysis{
		ProblemType:        "General Code Analysis",
		Complexity:         "Medium",
		Strategy:           "Investigate the codebase to understand the issue",
		RequiresCodeAccess: true,
		ToolSequence: []ToolRecommendation{
			{"repo_clone", "Access the codebase"},
			{"file_find", "Locate relevant files"},
			{"file_view", "Examine code context"},
		},
	}
}
