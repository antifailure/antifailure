# Safe State Page Overrides

> **PROJECT:** Antifailure
> **Generated:** 2026-08-30 15:29:33
> **Page Type:** General

> ⚠️ **IMPORTANT:** Rules in this file **override** the Master file (`design-system/MASTER.md`).
> Only deviations from the Master are documented here. For all other rules, refer to the Master.

---

## Page-Specific Rules

### Layout Overrides

- **Max Width:** 1200px (standard)
- **Layout:** Full-width sections, centered content

### Spacing Overrides

- No overrides — use Master spacing

### Typography Overrides

- No overrides — use Master typography

### Color Overrides

- No overrides — use Master colors

### Component Overrides

- Avoid: No visual feedback on current location
- Avoid: Static URLs for dynamic content
- Avoid: Depend on animationend or transitionend for required state correctness

---

## Page-Specific Components

- No unique components for this page

---

## Recommendations

- Effects: Clear focus rings (3-4px), ARIA labels, skip links, responsive design, reduced motion, 44x44px touch targets
- Navigation: Highlight active nav item with color/underline
- Navigation: Update URL on state/view changes
- Animation: Cancel or replace prior motion; set the final semantic state directly and handle cancellation cleanup
