# Modern CSS Master Reference (2026 Edition)

Source: Provided by Mike Z (project owner)
This is the CSS standard for all frontend work in this project. Use these features — do not fall back to older patterns when a modern equivalent exists.

---

## I. Architecture & Logic

### 1. Cascade Layers (`@layer`)
```css
@layer base, components, overrides;
@layer base { #nav { color: blue; } }
```

### 2. The Parent Selector (`:has()`)
```css
.card:has(img) { padding: 0; }
label:has(+ input:invalid) { color: red; }
```

### 3. Container Queries (`@container`)
```css
.sidebar { container-type: inline-size; }
@container (min-width: 400px) { .card { flex-direction: row; } }
```

### 4. Style Queries (`@container style(...)`)
```css
@container style(--theme: dark) { .text { color: white; } }
```

### 5. Scoping (`@scope`)
```css
@scope (.card) to (.card-content) { img { border-radius: 50%; } }
```

### 6. Native Nesting
```css
.card {
  background: white;
  & .title { font-weight: bold; }
  &:hover { background: #eee; }
}
```

### 7. Specificity Managers (`:is()` & `:where()`)
```css
:where(.card, .modal) { padding: 20px; } /* 0 specificity */
```

---

## II. Layout & Positioning

### 8. Anchor Positioning (`anchor()`)
```css
.tooltip { position-anchor: --my-btn; top: anchor(bottom); left: anchor(center); }
```

### 9. Subgrid
```css
.card { grid-row: span 3; display: grid; grid-template-rows: subgrid; }
```

### 10. CSS Masonry
```css
.grid { display: grid; grid-template-rows: masonry; }
```

### 11. Popover API
```css
#my-popover:popover-open { margin: auto; }
#my-popover::backdrop { background: rgba(0,0,0,0.5); }
```

### 12. Aspect Ratio
```css
.video { aspect-ratio: 16 / 9; }
```

### 13. Scrollbar Gutter
```css
html { scrollbar-gutter: stable; }
```

---

## III. Typography & Display

### 14. Fluid Sizing (`clamp()`)
```css
h1 { font-size: clamp(2rem, 5vw, 4rem); }
```

### 15. Text Wrapping (`text-wrap`)
```css
h1 { text-wrap: balance; }
p  { text-wrap: pretty; }
```

### 16. Field Sizing
```css
textarea { field-sizing: content; }
```

### 17. Text Box Trim
```css
h1 { text-box-trim: both; }
```

### 18. Content Visibility
```css
.heavy-section { content-visibility: auto; }
```

---

## IV. Animations & Interactions

### 19. View Transitions API
```css
::view-transition-old(root), ::view-transition-new(root) { animation-duration: 0.5s; }
```

### 20. Scroll-Driven Animations
```css
.progress-bar { animation: grow linear; animation-timeline: scroll(); }
```

### 21. Entry Animations (`@starting-style`)
```css
.dialog { transition: opacity 0.5s; display: none; }
.dialog.open { display: block; }
@starting-style { .dialog.open { opacity: 0; } }
```

### 22. Typed Custom Properties (`@property`)
```css
@property --my-color { syntax: '<color>'; inherits: false; initial-value: blue; }
.box { transition: --my-color 1s; }
```

### 23. Smart Validation (`:user-invalid`)
```css
input:user-invalid { border-color: red; }
```

---

## V. Colors & Theming

### 24. Relative Color Syntax
```css
background: rgb(from var(--brand) r g b / 50%);
```

### 25. Light/Dark Function
```css
:root { color-scheme: light dark; }
body { background: light-dark(white, #333); }
```

### 26. OKLCH Color Space
```css
color: oklch(70% 0.1 200);
```

### 27. Color Mix
```css
background: color-mix(in srgb, var(--brand), black 10%);
```
