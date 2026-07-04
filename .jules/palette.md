## 2025-05-15 - Standardizing Tooltip Usage on Icon Buttons
**Learning:** Discovered that `CustomIconButton` lacked built-in tooltip support, leading to potential inconsistency and accessibility gaps (missing `aria-label`). Adding it directly to the component ensures all icon buttons can easily be accessible.
**Action:** Check other 'Custom' components for missing standard accessibility props like `aria-label` or `tooltip`.

## 2025-05-16 - Accessible Clickable Images
**Learning:** Found `next/image` components being used as buttons with `onClick` handlers but without keyboard accessibility or semantic roles. This is a common pattern in older React code that needs remediation.
**Action:** When finding `onClick` on non-interactive elements, wrap them in a semantically correct `<button>` with reset styles and proper `aria-label`.

## 2025-05-17 - Icon Button Feedback Loop
**Learning:** Icon-only buttons like `CopyButton` often lack immediate visual feedback, leaving users unsure if the action succeeded. Adding a temporary state change (e.g., Check icon) is a low-cost, high-value micro-interaction.
**Action:** When auditing icon buttons, check if they provide feedback on click. If not, implement a temporary state change or tooltip update.
