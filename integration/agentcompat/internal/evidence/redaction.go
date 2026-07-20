package evidence

import (
	"regexp"
	"strings"
)

type redactionRule struct {
	pattern *regexp.Regexp
	replace string
}

var redactionRules = []redactionRule{
	{pattern: regexp.MustCompile(`(?i)(["']?authorization["']?\s*[:=]\s*["']?)[^"'\r\n,}]+(["']?)`), replace: `${1}[REDACTED]${2}`},
	{pattern: regexp.MustCompile(`(?i)(\b["']?(?:token|access[_-]?token|api[_-]?(?:key|token)|jwt[_-]?secret(?:[_-]?key)?|jwt[_-]?token|pat|agent[_-]?secret(?:[_-]?key)?|client[_-]?secret|handshake[_-]?secret|revert[_-]?handshake[_-]?secret|password|credential|transfer[_-]?token)["']?\s*[:=]\s*["']?)[^"'\s,;&}]+(["']?)`), replace: `${1}[REDACTED]${2}`},
	{pattern: regexp.MustCompile(`(?i)([?&](?:token|access_token|api_key|jwt|pat|secret|authorization|sig|signature|x-amz-signature)=)[^&#\s]+`), replace: `${1}[REDACTED]`},
	{pattern: regexp.MustCompile(`(?i)(/mcp/(?:download|upload)/)[A-Za-z0-9._-]+`), replace: `${1}[REDACTED]`},
	{pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`), replace: `[REDACTED]`},
	{pattern: regexp.MustCompile(`\b(?:ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`), replace: `[REDACTED]`},
	{pattern: regexp.MustCompile(`(?is)(<\s*(?:agent[_-]?secret(?:[_-]?key)?|authorization|transfer[_-]?token|password|credential|config[_-]?secret)\b)[^>]*?(/?)>`), replace: `${1} value="[REDACTED]"${2}>`},
	{pattern: regexp.MustCompile(`(?is)(<\s*(?:agent[_-]?secret(?:[_-]?key)?|authorization|transfer[_-]?token|password|credential|config[_-]?secret)\b[^>]*>)[^<]*(</\s*(?:agent[_-]?secret(?:[_-]?key)?|authorization|transfer[_-]?token|password|credential|config[_-]?secret)\s*>)`), replace: `${1}[REDACTED]${2}`},
	{pattern: regexp.MustCompile(`(?is)(<\s*(?:agent[_-]?secret(?:[_-]?key)?|authorization|transfer[_-]?token|password|credential|config[_-]?secret)\b[^>]*>)[^<]*$`), replace: `${1}[REDACTED]`},
	{pattern: regexp.MustCompile(`(?is)(<\s*(?:agent[_-]?secret(?:[_-]?key)?|authorization|transfer[_-]?token|password|credential|config[_-]?secret)\b)[^>]*$`), replace: `${1} value="[REDACTED]"`},
}

var sensitiveXMLAttribute = regexp.MustCompile(`(?i)(\b(?:agent[_-]?secret(?:[_-]?key)?|authorization|transfer[_-]?token|password|credential|config[_-]?secret)\b\s*=\s*)(?:"[^"]*"|'[^']*'|"[^\r\n>]*|'[^'\r\n>]*'|[^\s/>][^/>]*)`)

func Redact(input string) string {
	redacted := sensitiveXMLAttribute.ReplaceAllStringFunc(input, func(attribute string) string {
		separator := strings.IndexByte(attribute, '=')
		if separator < 0 {
			return attribute
		}
		prefix := attribute[:separator+1]
		return prefix + "\"[REDACTED]\""
	})
	for _, rule := range redactionRules {
		redacted = rule.pattern.ReplaceAllString(redacted, rule.replace)
	}
	return redacted
}
