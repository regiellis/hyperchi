package hyperchi

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"sync"
)

// AnimationConfig is an anime.js animation built in Go and passed to the page
// as JSON (ToJSON, AnimateTrigger).
type AnimationConfig struct {
	Targets  string         `json:"targets,omitempty"`
	Duration int            `json:"duration,omitempty"`
	Easing   string         `json:"easing,omitempty"`
	Delay    any            `json:"delay,omitempty"` // milliseconds, or a stagger config
	Loop     any            `json:"loop,omitempty"`  // true for forever, or an iteration count
	Props    map[string]any `json:"-"`               // animated properties, merged into ToJSON's object
}

// NewAnimation returns a 600 ms easeOutQuad animation of targets (a CSS
// selector).
func NewAnimation(targets string) *AnimationConfig {
	return &AnimationConfig{
		Targets:  targets,
		Duration: 600,
		Easing:   "easeOutQuad",
		Props:    make(map[string]any),
	}
}

// SetDuration sets the duration in milliseconds.
func (a *AnimationConfig) SetDuration(ms int) *AnimationConfig {
	a.Duration = ms
	return a
}

// SetEasing sets the anime.js easing name, such as linear, easeOutQuad, or
// easeInOutCubic.
func (a *AnimationConfig) SetEasing(easing string) *AnimationConfig {
	a.Easing = easing
	return a
}

// SetDelay sets the start delay in milliseconds.
func (a *AnimationConfig) SetDelay(ms int) *AnimationConfig {
	a.Delay = ms
	return a
}

// SetStaggerDelay delays each successive target by ms more than the last.
func (a *AnimationConfig) SetStaggerDelay(ms int) *AnimationConfig {
	a.Delay = map[string]int{"value": ms, "from": 0}
	return a
}

// SetLoop sets looping: true for forever, or an iteration count.
func (a *AnimationConfig) SetLoop(loop any) *AnimationConfig {
	a.Loop = loop
	return a
}

// FadeIn animates opacity from 0 to 1.
func (a *AnimationConfig) FadeIn() *AnimationConfig {
	a.Props["opacity"] = []float64{0, 1}
	return a
}

// FadeOut animates opacity from 1 to 0.
func (a *AnimationConfig) FadeOut() *AnimationConfig {
	a.Props["opacity"] = []float64{1, 0}
	return a
}

// SlideIn moves the target distance pixels into place and fades it in.
// direction is "up", "down", "left", or "right"; any other value only fades.
func (a *AnimationConfig) SlideIn(direction string, distance int) *AnimationConfig {
	switch direction {
	case "up":
		a.Props["translateY"] = []int{distance, 0}
	case "down":
		a.Props["translateY"] = []int{-distance, 0}
	case "left":
		a.Props["translateX"] = []int{distance, 0}
	case "right":
		a.Props["translateX"] = []int{-distance, 0}
	}
	a.FadeIn()
	return a
}

// Scale animates scale from from to to.
func (a *AnimationConfig) Scale(from, to float64) *AnimationConfig {
	a.Props["scale"] = []float64{from, to}
	return a
}

// Rotate rotates the target by degrees.
func (a *AnimationConfig) Rotate(degrees int) *AnimationConfig {
	a.Props["rotate"] = degrees
	return a
}

// Set adds an anime.js property, overriding any set before.
func (a *AnimationConfig) Set(key string, value any) *AnimationConfig {
	a.Props[key] = value
	return a
}

// ToJSON returns the anime.js parameter object as JSON: targets, duration,
// easing, delay, and loop when set, then Props.
func (a *AnimationConfig) ToJSON() (string, error) {
	config := make(map[string]any)
	config["targets"] = a.Targets
	if a.Duration > 0 {
		config["duration"] = a.Duration
	}
	if a.Easing != "" {
		config["easing"] = a.Easing
	}
	if a.Delay != nil {
		config["delay"] = a.Delay
	}
	if a.Loop != nil {
		config["loop"] = a.Loop
	}

	for k, v := range a.Props {
		config[k] = v
	}

	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ToHTMLAttr returns a data-anime attribute holding ToJSON's output, escaped
// for a quoted attribute value, or "" on error. Place it where an attribute
// goes: <div {{.animation}}>. The htmx-anime script reads data-anime.
func (a *AnimationConfig) ToHTMLAttr() template.HTMLAttr {
	json, err := a.ToJSON()
	if err != nil {
		return ""
	}
	return template.HTMLAttr(`data-anime="` + template.HTMLEscapeString(json) + `"`)
}

// AnimationPresets are ready-made animations for page transitions, component
// interactions, and staggered lists. Each takes the targets selector.
var AnimationPresets = struct {
	// Page enter animations
	PageSlideInFromLeft   func(targets string) *AnimationConfig
	PageSlideInFromRight  func(targets string) *AnimationConfig
	PageSlideInFromTop    func(targets string) *AnimationConfig
	PageSlideInFromBottom func(targets string) *AnimationConfig
	PageFadeIn            func(targets string) *AnimationConfig
	PageScaleIn           func(targets string) *AnimationConfig

	// Page exit animations
	PageSlideOutToLeft   func(targets string) *AnimationConfig
	PageSlideOutToRight  func(targets string) *AnimationConfig
	PageSlideOutToTop    func(targets string) *AnimationConfig
	PageSlideOutToBottom func(targets string) *AnimationConfig
	PageFadeOut          func(targets string) *AnimationConfig
	PageScaleOut         func(targets string) *AnimationConfig

	// Component interactions
	ComponentHover   func(targets string) *AnimationConfig
	ComponentTap     func(targets string) *AnimationConfig
	ComponentFadeIn  func(targets string) *AnimationConfig
	ComponentSlideIn func(targets string) *AnimationConfig

	// Fade and rise each target in turn, delayMs apart
	Stagger func(targets string, delayMs int) *AnimationConfig
}{
	PageSlideInFromLeft: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateX", []int{-100, 0}).
			FadeIn().
			SetDuration(400).
			SetEasing("easeOutCubic")
	},
	PageSlideInFromRight: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateX", []int{100, 0}).
			FadeIn().
			SetDuration(400).
			SetEasing("easeOutCubic")
	},
	PageSlideInFromTop: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateY", []int{-100, 0}).
			FadeIn().
			SetDuration(400).
			SetEasing("easeOutCubic")
	},
	PageSlideInFromBottom: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateY", []int{100, 0}).
			FadeIn().
			SetDuration(400).
			SetEasing("easeOutCubic")
	},
	PageFadeIn: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			FadeIn().
			SetDuration(300).
			SetEasing("easeOut")
	},
	PageScaleIn: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Scale(0.95, 1).
			FadeIn().
			SetDuration(300).
			SetEasing("easeOut")
	},

	PageSlideOutToLeft: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateX", []int{0, -100}).
			FadeOut().
			SetDuration(300).
			SetEasing("easeInCubic")
	},
	PageSlideOutToRight: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateX", []int{0, 100}).
			FadeOut().
			SetDuration(300).
			SetEasing("easeInCubic")
	},
	PageSlideOutToTop: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateY", []int{0, -100}).
			FadeOut().
			SetDuration(300).
			SetEasing("easeInCubic")
	},
	PageSlideOutToBottom: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateY", []int{0, 100}).
			FadeOut().
			SetDuration(300).
			SetEasing("easeInCubic")
	},
	PageFadeOut: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			FadeOut().
			SetDuration(200).
			SetEasing("easeIn")
	},
	PageScaleOut: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Scale(1, 0.95).
			FadeOut().
			SetDuration(200).
			SetEasing("easeIn")
	},

	ComponentHover: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Scale(1, 1.05).
			SetDuration(200).
			SetEasing("easeOut")
	},
	ComponentTap: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Scale(1, 0.95).
			SetDuration(100).
			SetEasing("easeOut")
	},
	ComponentFadeIn: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			FadeIn().
			SetDuration(200).
			SetEasing("easeOut")
	},
	ComponentSlideIn: func(targets string) *AnimationConfig {
		return NewAnimation(targets).
			Set("translateY", []int{20, 0}).
			FadeIn().
			SetDuration(300).
			SetEasing("easeOut")
	},

	Stagger: func(targets string, delayMs int) *AnimationConfig {
		return NewAnimation(targets).
			FadeIn().
			Set("translateY", []int{20, 0}).
			SetDuration(400).
			SetStaggerDelay(delayMs).
			SetEasing("easeOut")
	},
}

// AnimateTrigger returns an HX-Trigger header value that fires eventName with
// the animation as its detail: {"eventName": {...}}. eventName is not
// JSON-escaped, so pass a plain name.
func AnimateTrigger(eventName string, animation *AnimationConfig) (string, error) {
	json, err := animation.ToJSON()
	if err != nil {
		return "", err
	}
	return `{"` + eventName + `":` + json + `}`, nil
}

// PresetRegistry holds the presets added with RegisterPreset. It is safe for
// concurrent use.
type PresetRegistry struct {
	mu      sync.RWMutex
	presets map[string]func(targets string) *AnimationConfig
}

var registry = &PresetRegistry{
	presets: make(map[string]func(targets string) *AnimationConfig),
}

// RegisterPreset adds a named preset for GetPreset. A custom preset takes
// precedence over a built-in one with the same name.
func RegisterPreset(name string, fn func(targets string) *AnimationConfig) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.presets[name] = fn
}

// GetPreset builds the named preset for targets: a registered preset first,
// then a built-in one by its lower-camel name ("pageFadeIn",
// "componentHover"). The built-in Stagger has no name. An unknown name is an
// error.
func GetPreset(name string, targets string) (*AnimationConfig, error) {
	registry.mu.RLock()
	if fn, ok := registry.presets[name]; ok {
		registry.mu.RUnlock()
		return fn(targets), nil
	}
	registry.mu.RUnlock()

	switch name {
	case "pageSlideInFromLeft":
		return AnimationPresets.PageSlideInFromLeft(targets), nil
	case "pageSlideInFromRight":
		return AnimationPresets.PageSlideInFromRight(targets), nil
	case "pageSlideInFromTop":
		return AnimationPresets.PageSlideInFromTop(targets), nil
	case "pageSlideInFromBottom":
		return AnimationPresets.PageSlideInFromBottom(targets), nil
	case "pageFadeIn":
		return AnimationPresets.PageFadeIn(targets), nil
	case "pageScaleIn":
		return AnimationPresets.PageScaleIn(targets), nil
	case "pageSlideOutToLeft":
		return AnimationPresets.PageSlideOutToLeft(targets), nil
	case "pageSlideOutToRight":
		return AnimationPresets.PageSlideOutToRight(targets), nil
	case "pageSlideOutToTop":
		return AnimationPresets.PageSlideOutToTop(targets), nil
	case "pageSlideOutToBottom":
		return AnimationPresets.PageSlideOutToBottom(targets), nil
	case "pageFadeOut":
		return AnimationPresets.PageFadeOut(targets), nil
	case "pageScaleOut":
		return AnimationPresets.PageScaleOut(targets), nil
	case "componentHover":
		return AnimationPresets.ComponentHover(targets), nil
	case "componentTap":
		return AnimationPresets.ComponentTap(targets), nil
	case "componentFadeIn":
		return AnimationPresets.ComponentFadeIn(targets), nil
	case "componentSlideIn":
		return AnimationPresets.ComponentSlideIn(targets), nil
	default:
		return nil, fmt.Errorf("preset '%s' not found", name)
	}
}

// animeAttrEscaper makes the preset JSON safe to embed in a single-quoted JS
// string inside a double-quoted HTML attribute. Backslashes and single quotes
// are JS-escaped; quote, angle-bracket, and ampersand characters become JS
// unicode escapes, so the raw attribute value can never terminate the
// attribute, open a tag, or form an HTML entity. The JS lexer decodes the
// escapes back to the original characters before JSON.parse runs.
var animeAttrEscaper = strings.NewReplacer(
	"\\", "\\\\",
	"'", "\\'",
	"\"", "\\u0022",
	"<", "\\u003c",
	">", "\\u003e",
	"&", "\\u0026",
)

// AnimeAttr returns an x-init attribute that runs the named preset on the
// element, or "" for an unknown preset. Register it as a template function to
// write <div {{anime "pageFadeIn"}}>. x-init is an Alpine.js hook and does
// nothing without Alpine on the page. The JSON is escaped for the attribute
// context (see animeAttrEscaper).
func AnimeAttr(preset string) template.HTMLAttr {
	config, err := GetPreset(preset, "$el")
	if err != nil {
		return template.HTMLAttr("")
	}
	json, err := config.ToJSON()
	if err != nil {
		return template.HTMLAttr("")
	}
	return template.HTMLAttr(fmt.Sprintf(`x-init="anime(Object.assign(JSON.parse('%s'), {targets: $el}))"`,
		animeAttrEscaper.Replace(json)))
}

// AnimeData returns a data-anime-preset attribute naming preset, for a page
// script to pick up. preset is escaped for the attribute value.
func AnimeData(preset string) template.HTMLAttr {
	return template.HTMLAttr(`data-anime-preset="` + template.HTMLEscapeString(preset) + `"`)
}
