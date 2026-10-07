// Package response_targets renders htmx 4 hx-status:* attributes, which pick
// a different target or swap style by response status: an error can land in
// an error box while a success replaces the form.
package response_targets

import (
	"fmt"
	"html/template"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/regiellis/hyperchi/hyperchi/extensions"
)

// ResponseTargetsExtension registers template functions that render
// hx-status:* attributes. hx-status is built into htmx 4, so the extension
// ships no script. The attributes it produces look like this:
//
//	<button hx-post="/submit"
//	        hx-target="#success"
//	        hx-status:400="target:#validation-errors"
//	        hx-status:404="target:#not-found"
//	        hx-status:5xx="target:#server-error">
//	  Submit
//	</button>
type ResponseTargetsExtension struct {
	extensions.BaseExtension
}

// NewResponseTargetsExtension returns the extension.
func NewResponseTargetsExtension() *ResponseTargetsExtension {
	ext := &ResponseTargetsExtension{
		BaseExtension: extensions.BaseExtension{},
	}

	ext.SetName("response-targets").
		SetVersion("4.0.0").
		SetDescription("Target different elements based on HTTP response codes with hx-status").
		AddAttribute("hx-status:*")

	return ext
}

// Initialize adds the template functions: hxTarget<code> and hxSwap<code>
// for 200, 201, 400, 401, 403, 404, 422, 500, 502, and 503; the same for
// 2xx through 5xx; hxTargetError, hxSwapError, hxTargetSuccess, and
// hxSwapSuccess; and responseTargets, which takes a ResponseTargetConfig.
func (e *ResponseTargetsExtension) Initialize(registry *extensions.Registry) error {
	registry.AddTemplateFunc("hxTarget200", e.createTargetFunc("200"))
	registry.AddTemplateFunc("hxTarget201", e.createTargetFunc("201"))
	registry.AddTemplateFunc("hxTarget400", e.createTargetFunc("400"))
	registry.AddTemplateFunc("hxTarget401", e.createTargetFunc("401"))
	registry.AddTemplateFunc("hxTarget403", e.createTargetFunc("403"))
	registry.AddTemplateFunc("hxTarget404", e.createTargetFunc("404"))
	registry.AddTemplateFunc("hxTarget422", e.createTargetFunc("422"))
	registry.AddTemplateFunc("hxTarget500", e.createTargetFunc("500"))
	registry.AddTemplateFunc("hxTarget502", e.createTargetFunc("502"))
	registry.AddTemplateFunc("hxTarget503", e.createTargetFunc("503"))

	registry.AddTemplateFunc("hxSwap200", e.createSwapFunc("200"))
	registry.AddTemplateFunc("hxSwap201", e.createSwapFunc("201"))
	registry.AddTemplateFunc("hxSwap400", e.createSwapFunc("400"))
	registry.AddTemplateFunc("hxSwap401", e.createSwapFunc("401"))
	registry.AddTemplateFunc("hxSwap403", e.createSwapFunc("403"))
	registry.AddTemplateFunc("hxSwap404", e.createSwapFunc("404"))
	registry.AddTemplateFunc("hxSwap422", e.createSwapFunc("422"))
	registry.AddTemplateFunc("hxSwap500", e.createSwapFunc("500"))
	registry.AddTemplateFunc("hxSwap502", e.createSwapFunc("502"))
	registry.AddTemplateFunc("hxSwap503", e.createSwapFunc("503"))

	registry.AddTemplateFunc("hxTarget2xx", e.createTargetFunc("2*"))
	registry.AddTemplateFunc("hxTarget3xx", e.createTargetFunc("3*"))
	registry.AddTemplateFunc("hxTarget4xx", e.createTargetFunc("4*"))
	registry.AddTemplateFunc("hxTarget5xx", e.createTargetFunc("5*"))

	registry.AddTemplateFunc("hxSwap2xx", e.createSwapFunc("2*"))
	registry.AddTemplateFunc("hxSwap3xx", e.createSwapFunc("3*"))
	registry.AddTemplateFunc("hxSwap4xx", e.createSwapFunc("4*"))
	registry.AddTemplateFunc("hxSwap5xx", e.createSwapFunc("5*"))

	registry.AddTemplateFunc("hxTargetError", e.createTargetFunc("4*,5*"))
	registry.AddTemplateFunc("hxSwapError", e.createSwapFunc("4*,5*"))
	registry.AddTemplateFunc("hxTargetSuccess", e.createTargetFunc("2*"))
	registry.AddTemplateFunc("hxSwapSuccess", e.createSwapFunc("2*"))

	registry.AddTemplateFunc("responseTargets", e.responseTargets)

	return nil
}

// createTargetFunc returns a template function rendering
// hx-status:<code>="target:<selector>" for each comma-separated code.
func (e *ResponseTargetsExtension) createTargetFunc(code string) func(string) template.HTMLAttr {
	return func(target string) template.HTMLAttr {
		return statusAttrs(code, "target", target)
	}
}

// createSwapFunc returns a template function rendering
// hx-status:<code>="swap:<style>" for each comma-separated code.
func (e *ResponseTargetsExtension) createSwapFunc(code string) func(string) template.HTMLAttr {
	return func(swap string) template.HTMLAttr {
		return statusAttrs(code, "swap", swap)
	}
}

// ResponseTargetConfig is the input of the responseTargets template function.
// Map keys are status codes or the patterns htmx 4 matches: "404", "40x",
// "4xx", or "4*" for "4xx", with an uppercase X allowed. A key becomes part
// of an attribute name, so any other key fails the render with an error.
type ResponseTargetConfig struct {
	// Target is the hx-target for responses no status entry matches.
	Target string `json:"target,omitempty"`

	// Swap is the hx-swap for responses no status entry matches.
	Swap string `json:"swap,omitempty"`

	// Targets maps a status pattern to its target selector.
	Targets map[string]string `json:"targets,omitempty"`

	// Swaps maps a status pattern to its swap style.
	Swaps map[string]string `json:"swaps,omitempty"`
}

// responseTargets renders hx-target, hx-swap, and one hx-status attribute per
// map entry, Targets before Swaps, each sorted by key. Values are
// HTML-escaped; a key that is not a status pattern returns an error.
func (e *ResponseTargetsExtension) responseTargets(config ResponseTargetConfig) (template.HTMLAttr, error) {
	var parts []string

	if config.Target != "" {
		parts = append(parts, fmt.Sprintf(`hx-target="%s"`, template.HTMLEscapeString(config.Target)))
	}

	if config.Swap != "" {
		parts = append(parts, fmt.Sprintf(`hx-swap="%s"`, template.HTMLEscapeString(config.Swap)))
	}

	for _, entries := range []struct {
		key    string
		values map[string]string
	}{{"target", config.Targets}, {"swap", config.Swaps}} {
		for _, code := range slices.Sorted(maps.Keys(entries.values)) {
			pattern, err := statusPattern(code)
			if err != nil {
				return "", err
			}
			parts = append(parts, fmt.Sprintf(`hx-status:%s="%s:%s"`, pattern, entries.key, template.HTMLEscapeString(entries.values[code])))
		}
	}

	return template.HTMLAttr(strings.Join(parts, " ")), nil
}

// statusPatternRe matches what htmx 4 looks up for a response status: the
// code, the code with its last digit as x, or the class as Nxx.
var statusPatternRe = regexp.MustCompile(`^[1-5](?:[0-9]{2}|[0-9]x|xx)$`)

// statusPattern normalizes code (see normalizeStatusPattern, and lowercase)
// and returns an error when the result is not a pattern htmx matches.
func statusPattern(code string) (string, error) {
	pattern := strings.ToLower(normalizeStatusPattern(code))
	if !statusPatternRe.MatchString(pattern) {
		return "", fmt.Errorf("responseTargets: %q is not a status pattern such as 404, 40x, or 4xx", code)
	}
	return pattern, nil
}

// ErrorHandling renders hx-target for success and sends 4xx and 5xx
// responses to errorTarget.
func ErrorHandling(successTarget, errorTarget string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(
		`hx-target="%s" hx-status:4xx="target:%s" hx-status:5xx="target:%s"`,
		template.HTMLEscapeString(successTarget),
		template.HTMLEscapeString(errorTarget),
		template.HTMLEscapeString(errorTarget),
	))
}

// FormValidation renders hx-target for success and sends 422 responses, the
// status a failed hyperchi.Bind returns, to validationTarget.
func FormValidation(successTarget, validationTarget string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(
		`hx-target="%s" hx-status:422="target:%s"`,
		template.HTMLEscapeString(successTarget),
		template.HTMLEscapeString(validationTarget),
	))
}

// AuthHandling renders hx-target for success and sends 401 and 403
// responses to loginTarget.
func AuthHandling(successTarget, loginTarget string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(
		`hx-target="%s" hx-status:401="target:%s" hx-status:403="target:%s"`,
		template.HTMLEscapeString(successTarget),
		template.HTMLEscapeString(loginTarget),
		template.HTMLEscapeString(loginTarget),
	))
}

// RESTfulCRUD renders hx-target for success, sends 404 to notFoundTarget,
// and sends other 4xx and 5xx responses to errorTarget.
func RESTfulCRUD(successTarget, errorTarget, notFoundTarget string) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(
		`hx-target="%s" hx-status:404="target:%s" hx-status:4xx="target:%s" hx-status:5xx="target:%s"`,
		template.HTMLEscapeString(successTarget),
		template.HTMLEscapeString(notFoundTarget),
		template.HTMLEscapeString(errorTarget),
		template.HTMLEscapeString(errorTarget),
	))
}

// normalizeStatusPattern turns "2*" through "5*" into htmx's "2xx" through
// "5xx" and returns anything else unchanged.
func normalizeStatusPattern(code string) string {
	switch code {
	case "2*":
		return "2xx"
	case "3*":
		return "3xx"
	case "4*":
		return "4xx"
	case "5*":
		return "5xx"
	default:
		return code
	}
}

// statusAttrs renders hx-status:<pattern>="<key>:<value>" for each
// comma-separated pattern, with value HTML-escaped.
func statusAttrs(patterns, key, value string) template.HTMLAttr {
	var parts []string
	for _, pattern := range strings.Split(patterns, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			`hx-status:%s="%s:%s"`,
			normalizeStatusPattern(pattern),
			key,
			template.HTMLEscapeString(value),
		))
	}
	return template.HTMLAttr(strings.Join(parts, " "))
}
