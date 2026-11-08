# Provider logos

Brand logos of the model providers shown on the Router pages.

- Source: the LobeHub icon set, npm package `@lobehub/icons-static-svg`
  version 1.102.0 (https://github.com/lobehub/lobe-icons).
- Files: `https://unpkg.com/@lobehub/icons-static-svg@1.102.0/icons/<name>.svg`,
  copied unchanged.
- License: MIT, see `LICENSE` in this folder (copied from the lobe-icons
  repository).

The logos are trademarks of their owners and are used here only to name the
provider. Monochrome files use `currentColor` and follow the theme; the
`-color` files keep the brand colors. `ProviderLogo`
(`src/views/router/provider-logo.tsx`) inlines them and falls back to a
two-letter monogram for any other provider.
