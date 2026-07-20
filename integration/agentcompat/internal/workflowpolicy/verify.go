package workflowpolicy

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	secretContextPattern       = regexp.MustCompile(`(?i)\bsecrets\b`)
	tokenSourcePattern         = regexp.MustCompile(`(?i)\bgithub\s*\.\s*token\b|\bGITHUB_TOKEN\b`)
	untrustedExpressionPattern = regexp.MustCompile(`\$\{\{[^}]*(?:github\s*\.\s*event\s*\.|github\s*\.\s*head_ref|github\s*\[)[^}]*\}\}`)
)

type checker struct {
	repository Repository
	violations []Violation
}

func Verify(data []byte, repository Repository) error {
	return verify("memory", data, repository)
}

func VerifyFile(path string, repository Repository) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &ReadError{Path: path, Cause: err}
	}
	return verify(path, data, repository)
}

func verify(source string, data []byte, repository Repository) error {
	root, err := parseWorkflow(source, data)
	if err != nil {
		return err
	}
	policyChecker := checker{repository: repository}
	policyChecker.checkWorkflow(root)
	if len(policyChecker.violations) > 0 {
		return &PolicyError{violations: policyChecker.violations}
	}
	return nil
}

func (c *checker) checkWorkflow(root *yaml.Node) {
	if c.repository != RepositoryAgent && c.repository != RepositoryNezha {
		c.reject(RuleRepositoryNotAllowed, at("$", root), fmt.Sprintf("current repository %q is not supported", c.repository))
	}
	c.checkTriggers(root)
	c.checkSecretContexts(root)
	c.checkUntrustedExpressions(root)
	c.checkForbiddenEnvironment(root)
	c.checkPermissions(root, "$.permissions", true)
	concurrency, exists := mappingValue(root, "concurrency")
	if !exists || !validConcurrency(concurrency) {
		node := root
		if exists {
			node = concurrency
		}
		detail := "workflow concurrency requires a nonempty group"
		if exists {
			if _, hasGroup := mappingValue(concurrency, "group"); hasGroup {
				cancel, hasCancel := mappingValue(concurrency, "cancel-in-progress")
				if hasCancel && (cancel.Kind != yaml.ScalarNode || cancel.Tag != "!!bool") {
					detail = "workflow concurrency cancel-in-progress must be a boolean"
				}
			}
		}
		c.reject(RuleMissingConcurrency, at("$.concurrency", node), detail)
	}
	jobs, exists := mappingValue(root, "jobs")
	if !exists || jobs.Kind != yaml.MappingNode || len(jobs.Content) == 0 {
		node := root
		if exists {
			node = jobs
		}
		c.reject(RuleWorkflowStructure, at("$.jobs", node), "workflow jobs must be a nonempty mapping")
		return
	}
	for _, entry := range mappingEntries(jobs) {
		if entry[1].Kind != yaml.MappingNode {
			c.reject(RuleWorkflowStructure, at("$.jobs."+entry[0].Value, entry[1]), "workflow job must be a mapping")
			continue
		}
		c.checkJob(entry[0].Value, entry[1])
	}
	c.checkRequiredAggregator(jobs)
}

func (c *checker) checkForbiddenEnvironment(root *yaml.Node) {
	walkMappings(root, func(mapping *yaml.Node) {
		environment, exists := mappingValue(mapping, "env")
		if !exists || environment.Kind != yaml.MappingNode {
			return
		}
		for _, entry := range mappingEntries(environment) {
			if strings.HasPrefix(strings.ToUpper(entry[0].Value), "GIT_") {
				c.reject(RuleRepositoryNotLiteral, at("$.env."+entry[0].Value, entry[0]), fmt.Sprintf("Git configuration environment %s is forbidden", entry[0].Value))
			}
		}
	})
}

func validConcurrency(node *yaml.Node) bool {
	if value, literal := scalarString(node); literal {
		return strings.TrimSpace(value) != ""
	}
	group, exists := mappingValue(node, "group")
	if !exists {
		return false
	}
	value, literal := scalarString(group)
	if !literal || strings.TrimSpace(value) == "" {
		return false
	}
	cancel, exists := mappingValue(node, "cancel-in-progress")
	return !exists || (cancel.Kind == yaml.ScalarNode && cancel.Tag == "!!bool")
}

func (c *checker) checkTriggers(root *yaml.Node) {
	trigger, exists := mappingValue(root, "on")
	if !exists {
		return
	}
	for _, forbidden := range []string{"pull_request_target", "workflow_run"} {
		if containsScalar(trigger, forbidden) {
			c.reject(RulePrivilegedTrigger, at("$.on."+forbidden, trigger), fmt.Sprintf("privileged trigger %s is forbidden", forbidden))
		}
	}
}

func (c *checker) checkSecretContexts(root *yaml.Node) {
	walkScalars(root, func(node *yaml.Node) {
		if strings.Contains(node.Value, "${{") && (secretContextPattern.MatchString(node.Value) || tokenSourcePattern.MatchString(node.Value)) {
			detail := "secrets context is forbidden"
			if tokenSourcePattern.MatchString(node.Value) {
				detail = "github.token secret source is forbidden"
			}
			c.reject(RuleSecretContext, at("$", node), detail)
		}
	})
	walkMappings(root, func(mapping *yaml.Node) {
		for _, entry := range mappingEntries(mapping) {
			if strings.EqualFold(entry[0].Value, "GITHUB_TOKEN") {
				c.reject(RuleSecretContext, at("$.env.GITHUB_TOKEN", entry[0]), "GITHUB_TOKEN secret source is forbidden")
			}
		}
	})
}

func (c *checker) checkUntrustedExpressions(root *yaml.Node) {
	walkScalars(root, func(node *yaml.Node) {
		if expression := untrustedExpressionPattern.FindString(node.Value); expression != "" {
			c.reject(RuleUntrustedExpression, at("$", node), fmt.Sprintf("untrusted github event expression %s is forbidden", expression))
		}
	})
}

func (c *checker) checkPermissions(mapping *yaml.Node, path string, required bool) {
	permissions, exists := mappingValue(mapping, "permissions")
	if !exists {
		if required {
			c.reject(RuleWritePermission, at(path, mapping), "root permissions must be explicitly read-only")
		}
		return
	}
	if permissions.Kind == yaml.ScalarNode {
		if permissions.Value != "read-all" {
			c.reject(RuleWritePermission, at(path, permissions), fmt.Sprintf("permissions must be read-only, got %q", permissions.Value))
		}
		return
	}
	if permissions.Kind != yaml.MappingNode {
		c.reject(RuleWritePermission, at(path, permissions), "permissions must be read-all or a read-only mapping")
		return
	}
	for _, entry := range mappingEntries(permissions) {
		if entry[1].Kind != yaml.ScalarNode {
			c.reject(RuleWritePermission, at(path+"."+entry[0].Value, entry[1]), "permission value must be a scalar read or none")
			continue
		}
		value := strings.ToLower(strings.TrimSpace(entry[1].Value))
		if value != "read" && value != "none" {
			c.reject(RuleWritePermission, at(path+"."+entry[0].Value, entry[1]), fmt.Sprintf("permission %s must be read or none, got %q", entry[0].Value, entry[1].Value))
		}
	}
}

func (c *checker) reject(rule Rule, location violationLocation, detail string) {
	c.violations = append(c.violations, Violation{
		Rule: rule, Path: location.path, Line: location.node.Line, Column: location.node.Column,
		Detail: detail,
	})
}
