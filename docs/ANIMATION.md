# Animation with AnimeJS

HyperChi provides seamless integration with [AnimeJS](https://animejs.com/) for creating smooth, professional animations. The integration supports server-driven animations, declarative syntax, and works with HTMX 4.

## Why AnimeJS?

- **Perfect for HTMX**: Animates real DOM elements, works seamlessly with HTMX content swaps
- **Lightweight**: ~9KB gzipped, hardware-accelerated
- **Server-Driven**: Animation logic can be defined server-side and executed client-side
- **Timeline Control**: Essential for game-like sequences and complex choreography
- **Creative Power**: Staggered animations, SVG morphing, path animations

## Installation

AnimeJS is included in HyperChi by default. If you need to install it manually:

```bash
npm install animejs
```

## Quick Start

### 1. Server-Side Animation Configuration

```go
app.Get("/", func(c *hyperchi.Context) error {
    // Create animation config
    anim := hyperchi.NewAnimation("#new-content").
        FadeIn().
        SlideIn("up", 20).
        SetDuration(600)

    // ToHTMLAttr is a whole data-anime="..." attribute, escaped for HTML
    return c.View("index", hyperchi.H{"animation": anim.ToHTMLAttr()})
})
```

### 2. Client-Side Template Usage

```html
<!-- Declarative animation: .animation renders as data-anime="..." -->
<div id="new-content" {{.animation}}>
    Content will fade in and slide up
</div>

<!-- Or use presets -->
<div data-anime-preset="fadeIn">
    Simple fade in animation
</div>
```

### 3. HTMX Integration

```html
<!-- Animate content after HTMX swap -->
<button hx-get="/content"
        hx-target="#output"
        hx-swap="innerHTML">
    Load Content
</button>

<div id="output" data-anime-preset="slideInUp">
    <!-- New content will animate in -->
</div>
```

## Zero-Config HTMX Extensions

HyperChi includes powerful HTMX extensions for common animation patterns with zero configuration needed.

### Modal Extension

Automatically animates modals with backdrop fade and content transitions.

```html
<!-- Basic modal with default animations -->
<div data-anime-extension="modal" role="dialog">
    <div class="modal-backdrop"></div>
    <div class="modal-content">
        <!-- Your modal content -->
    </div>
</div>

<!-- Custom enter/exit animations -->
<div data-anime-extension="modal"
     data-modal-enter="slideDown"
     data-modal-exit="slideUp"
     role="dialog">
    <!-- Modal content -->
</div>
```

Available presets: `default`, `slideDown`, `zoom`

### List Extension

Smooth FLIP animations when list items are added, removed, or reordered.

```html
<!-- Animated list with staggered entrance -->
<ul hx-get="/items"
    hx-trigger="load"
    data-anime-extension="list"
    data-list-stagger="50"
    data-list-duration="400">
    <!-- List items -->
</ul>

<!-- Each item needs an id or data-id for position tracking -->
<li id="item-1">Item 1</li>
<li id="item-2">Item 2</li>
```

### Fade-Swap Extension

Smooth fade transition for all HTMX content swaps.

```html
<!-- All swaps within this element will fade -->
<div data-anime-extension="fade" data-fade-duration="300">
    <button hx-get="/content" hx-target="#result">Load</button>
    <div id="result"><!-- Content will fade in/out --></div>
</div>
```

### Scroll Animation Extension

Animate elements when they scroll into viewport using IntersectionObserver.

```html
<!-- Fade in when scrolled into view -->
<section data-anime-extension="scroll" data-scroll-preset="fadeIn">
    <!-- Content animates on scroll -->
</section>

<!-- Slide up with custom threshold -->
<div data-anime-extension="scroll"
     data-scroll-preset="slideUp"
     data-scroll-threshold="0.3"
     data-scroll-once="true">
    <!-- Animates once at 30% visibility -->
</div>
```

Available scroll presets: `fadeIn`, `slideUp`, `slideLeft`, `scaleIn`, `componentFadeIn`

## Animation API

### Go-Side Configuration

#### Creating Animations

```go
// Basic animation
anim := hyperchi.NewAnimation("#target")

// With properties
timed := hyperchi.NewAnimation("#target").
    SetDuration(800).
    SetEasing("easeOutCubic").
    SetDelay(200)
```

#### Animation Presets

HyperChi includes Framer Motion-inspired presets organized by use case:

```go
// Page Transitions - Enter Animations
hyperchi.AnimationPresets.PageSlideInFromLeft("#page")
hyperchi.AnimationPresets.PageSlideInFromRight("#page")
hyperchi.AnimationPresets.PageSlideInFromTop("#page")
hyperchi.AnimationPresets.PageSlideInFromBottom("#page")
hyperchi.AnimationPresets.PageFadeIn("#page")
hyperchi.AnimationPresets.PageScaleIn("#page")

// Page Transitions - Exit Animations
hyperchi.AnimationPresets.PageSlideOutToLeft("#page")
hyperchi.AnimationPresets.PageSlideOutToRight("#page")
hyperchi.AnimationPresets.PageSlideOutToTop("#page")
hyperchi.AnimationPresets.PageSlideOutToBottom("#page")
hyperchi.AnimationPresets.PageFadeOut("#page")
hyperchi.AnimationPresets.PageScaleOut("#page")

// Component Micro-Interactions
hyperchi.AnimationPresets.ComponentHover(".button")
hyperchi.AnimationPresets.ComponentTap(".button")
hyperchi.AnimationPresets.ComponentFadeIn(".card")
hyperchi.AnimationPresets.ComponentSlideIn(".card")

// Utility Animations
hyperchi.AnimationPresets.Stagger(".list-item", 50) // 50ms stagger delay
```

#### Building Custom Animations

```go
anim := hyperchi.NewAnimation(".card").
    FadeIn().                           // Opacity: 0 → 1
    SlideIn("up", 30).                  // TranslateY: 30 → 0
    SetDuration(600).                   // 600ms duration
    SetEasing("easeOutQuad").           // Easing function
    SetStaggerDelay(100)                // 100ms stagger for multiple elements
```

#### Custom Properties

```go
anim := hyperchi.NewAnimation("#box").
    Set("rotate", "1turn").             // Rotate 360deg
    Set("backgroundColor", "#FF0000").  // Change color
    Set("borderRadius", []string{"0%", "50%"}) // Morph to circle
```

#### Custom Preset Registry

Register your own reusable animation presets with thread-safe storage:

```go
// Register a custom preset
hyperchi.RegisterPreset("myFancyAnimation", func(targets string) *hyperchi.AnimationConfig {
    return hyperchi.NewAnimation(targets).
        Set("rotate", 360).
        Set("scale", []float64{1, 1.2, 1}).
        FadeIn().
        SetDuration(800).
        SetEasing("easeInOutElastic")
})

// Use your custom preset
anim, err := hyperchi.GetPreset("myFancyAnimation", ".element")
if err != nil {
    // Handle preset not found
}

// Or use directly in templates with the helper
data := helpers.NewJSON().
    Set("customAnim", hyperchi.AnimeAttr("myFancyAnimation"))
```

#### Template Helpers

Convenient template functions for declarative animations:

```go
// AnimeAttr - Creates an initialization attribute with animation
data := helpers.NewJSON().
    Set("fadeAnim", hyperchi.AnimeAttr("pageFadeIn"))

// Template usage:
// <div {{.fadeAnim}}>Content</div>
// Renders: <div x-init="anime(...)">Content</div> (needs Alpine.js on the page)

// AnimeData - Creates data-anime-preset attribute
slideData := helpers.NewJSON().
    Set("slideAnim", hyperchi.AnimeData("componentSlideIn"))

// Template usage:
// <div {{.slideAnim}}>Content</div>
// Renders: <div data-anime-preset="componentSlideIn">Content</div>
// The preset name is HTML-escaped.
```

### Client-Side Usage

#### Data Attributes

```html
<!-- JSON configuration -->
<div data-anime='{
    "opacity": [0, 1],
    "translateY": [-20, 0],
    "duration": 600,
    "easing": "easeOutQuad"
}'>Content</div>

<!-- Preset -->
<div data-anime-preset="fadeIn">Content</div>
```

#### Programmatic Usage

```html
<button onclick="HyperAnime.init(this, { rotate: '1turn', duration: 1000 })">
    Spin Me
</button>
```

#### HTMX Events

```html
<!-- Animate on HTMX after-swap -->
<div hx-get="/data"
     hx-trigger="click"
     hx-swap="innerHTML"
     hx-on:htmx:after-swap="
        HyperAnime.init(event.detail.ctx.target, {
            opacity: [0, 1],
            duration: 400
        })
     ">
    Click to load
</div>
```

## Server-Driven Animation Triggers

### Using HX-Trigger Header

```go
app.Get("/content", func(c *hyperchi.Context) error {
    // Create animation config
    anim := hyperchi.NewAnimation("#content").
        FadeIn().
        SetDuration(600)

    // AnimateTrigger builds the HX-Trigger value, including custom properties
    trigger, err := hyperchi.AnimateTrigger("animateIn", anim)
    if err != nil {
        return err
    }
    return c.SetHeader("HX-Trigger", trigger).Fragment("content", nil)
})
```

Use `AnimateTrigger` with `c.SetHeader` rather than passing the config to `c.HX().Trigger`:
the config's custom properties (`Props`) only serialize through `ToJSON`.

The client-side integration will automatically handle the `animateIn` event and execute the animation.

## Animation Patterns

### 1. List Item Stagger

```go
// Server-side
anim := hyperchi.NewAnimation(".list-item").
    FadeIn().
    SlideIn("up", 20).
    SetStaggerDelay(50). // 50ms delay between each item
    SetDuration(400)
data := hyperchi.H{"items": items, "staggerAnim": anim.ToHTMLAttr()}
```

```html
<!-- Template -->
{{range .items}}
<div class="list-item" {{$.staggerAnim}}>
    {{.name}}
</div>
{{end}}
```

### 2. Loading Spinner to Content

```go
// Handler returns content with animation trigger
app.Get("/load", func(c *hyperchi.Context) error {
    anim := hyperchi.AnimationPresets.ComponentFadeIn("#content")
    trigger, err := hyperchi.AnimateTrigger("animateIn", anim)
    if err != nil {
        return err
    }
    return c.SetHeader("HX-Trigger", trigger).Fragment("loaded-content", nil)
})
```

```html
<!-- HTMX triggers the load -->
<div hx-get="/load" hx-target="#container">
    <div id="container">
        <div class="spinner">Loading...</div>
    </div>
</div>
```

### 3. Sequential Animations

```html
<div hc-data="{
    sequence() {
        const timeline = anime.timeline();

        timeline
            .add({
                targets: '.step-1',
                opacity: [0, 1],
                translateY: [20, 0],
                duration: 400
            })
            .add({
                targets: '.step-2',
                opacity: [0, 1],
                translateY: [20, 0],
                duration: 400
            })
            .add({
                targets: '.step-3',
                opacity: [0, 1],
                translateY: [20, 0],
                duration: 400
            });
    }
}">
    <button hc-on:click="sequence()">Run Sequence</button>
    <div class="step-1">First</div>
    <div class="step-2">Second</div>
    <div class="step-3">Third</div>
</div>
```

### 4. Morphing Shapes (SVG)

```go
anim := hyperchi.NewAnimation("#morph-path").
    Set("d", "M10,10 L90,90").  // SVG path morphing
    SetDuration(1000).
    SetEasing("easeInOutQuad")
```

### 5. Scroll-Triggered Animations

The scroll extension covers this without a component:

```html
<div data-anime-extension="scroll" data-scroll-preset="slideUp">
    Content appears on scroll
</div>
```

## Easing Functions

Common easing functions available:

- **Linear**: `linear`
- **Quad**: `easeInQuad`, `easeOutQuad`, `easeInOutQuad`
- **Cubic**: `easeInCubic`, `easeOutCubic`, `easeInOutCubic`
- **Quart**: `easeInQuart`, `easeOutQuart`, `easeInOutQuart`
- **Quint**: `easeInQuint`, `easeOutQuint`, `easeInOutQuint`
- **Sine**: `easeInSine`, `easeOutSine`, `easeInOutSine`
- **Expo**: `easeInExpo`, `easeOutExpo`, `easeInOutExpo`
- **Circ**: `easeInCirc`, `easeOutCirc`, `easeInOutCirc`
- **Back**: `easeInBack`, `easeOutBack`, `easeInOutBack`
- **Elastic**: `easeInElastic`, `easeOutElastic`, `easeInOutElastic`
- **Bounce**: `easeInBounce`, `easeOutBounce`, `easeInOutBounce`

Custom easing: `easeOutElastic(1, .8)` (amplitude, period)

## Performance Tips

1. **Use transforms** for better performance:
   ```go
   // Good - GPU accelerated
   anim.Set("translateX", []int{0, 100})

   // Avoid - triggers layout
   anim.Set("left", []string{"0px", "100px"})
   ```

2. **Batch animations**:
   ```go
   // Animate multiple properties together
   anim.FadeIn().SlideIn("up", 20)
   ```

3. **Use will-change for complex animations**:
   ```html
   <div style="will-change: transform, opacity" data-anime-preset="zoomIn">
       Complex animation
   </div>
   ```

4. **Stagger for lists** instead of individual animations:
   ```go
   anim.SetStaggerDelay(50) // One animation, multiple elements
   ```

## Advanced Examples

### Game-Like Sequence

```go
app.Post("/game/sequence", func(c *hyperchi.Context) error {
    // Create timeline animation
    timeline := hyperchi.H{
        "targets": ".game-element",
        "keyframes": []hyperchi.H{
            {"translateX": 100, "duration": 500},
            {"rotateZ": 180, "duration": 300},
            {"translateY": 100, "duration": 500},
            {"scale": 1.5, "duration": 200},
        },
        "easing": "easeOutExpo",
    }

    // A trigger with detail sends HX-Trigger: {"animate": {...}}
    c.HX().Trigger("animate", timeline)
    return c.Text("Game sequence triggered!")
})
```

### Interactive Hover Animation

```html
<div id="hover-card" class="card"
     hc-data="{
        hover(scale, boxShadow) {
            anime({
                targets: '#hover-card',
                scale: scale,
                boxShadow: boxShadow,
                duration: 300,
                easing: 'easeOutQuad'
            })
        }
     }"
     hc-on:mouseenter="hover(1.05, '0 10px 30px rgba(0,0,0,0.2)')"
     hc-on:mouseleave="hover(1, '0 2px 10px rgba(0,0,0,0.1)')">
    Hover me
</div>
```

## API Reference

### Go Types

```go
type AnimationConfig struct {
    Targets  string
    Duration int
    Easing   string
    Delay    interface{}
    Loop     interface{}
    Props    map[string]interface{}
}
```

### Go Methods

- `NewAnimation(targets string) *AnimationConfig`
- `SetDuration(ms int) *AnimationConfig`
- `SetEasing(easing string) *AnimationConfig`
- `SetDelay(ms int) *AnimationConfig`
- `SetStaggerDelay(ms int) *AnimationConfig`
- `SetLoop(loop interface{}) *AnimationConfig`
- `FadeIn() *AnimationConfig`
- `FadeOut() *AnimationConfig`
- `SlideIn(direction string, distance int) *AnimationConfig`
- `Scale(from, to float64) *AnimationConfig`
- `Rotate(degrees int) *AnimationConfig`
- `Set(key string, value interface{}) *AnimationConfig`
- `ToJSON() (string, error)`
- `ToHTMLAttr() template.HTMLAttr`

### JavaScript API

```javascript
// Direct anime.js usage
anime({
    targets: '.element',
    translateX: 250,
    duration: 800
})

// HyperAnime helper
HyperAnime.init(element, config)
HyperAnime.process(rootElement)
HyperAnime.presets.fadeIn('#element')
```

## Resources

- **AnimeJS Documentation**: https://animejs.com/documentation/
- **AnimeJS Codepen Examples**: https://codepen.io/collection/XLebem/
- **HyperChi Animation Examples**: See `examples/` directory

---

**Remember**: Animation should enhance UX, not distract. Use sparingly and purposefully. Your users will appreciate smooth, subtle animations that guide their attention rather than overwhelming it.
