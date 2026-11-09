# Channel logos

Brand logos of the chat channels shown on the Channels page and wherever a
session, agent, or calendar action names its channel.

## Telegram, Discord, WhatsApp

- Source: Simple Icons, npm package `simple-icons` version 16.36.0
  (https://github.com/simple-icons/simple-icons).
- Files: `https://cdn.jsdelivr.net/npm/simple-icons@16.36.0/icons/<slug>.svg`
  for the slugs `telegram`, `discord`, and `whatsapp`, copied unchanged.
- License: CC0 1.0 Universal, see `LICENSE` in this folder (copied from
  `simple-icons@16.36.0/LICENSE.md`).

## Slack

Simple Icons no longer ships a Slack icon (the slug 404s in 16.36.0), and the
LobeHub set used for the provider logos has none either.

- Source: Devicon, npm package `devicon` version 2.18.0
  (https://github.com/devicons/devicon).
- File: `https://cdn.jsdelivr.net/npm/devicon@2.18.0/icons/slack/slack-original.svg`,
  copied unchanged as `slack-color.svg` (the multicolor mark).
- License: MIT, see `LICENSE-devicon` in this folder (copied from
  `devicon@2.18.0/LICENSE`).

## Use

The logos are trademarks of their owners and are used here only to name the
channel. Each file was checked before it was vendored: one `<svg>` with
`<path>` (and `<title>`) elements only, with no scripts, event handlers,
links, styles, or external references.

`ChannelLogo` (`src/views/channel-logo.tsx`) inlines them. At tile sizes it
draws an app-icon tile: the white glyph on the brand color (Telegram #26A5E4,
Discord #5865F2, WhatsApp #25D366; for Telegram only the plane, without the
outer circle) and the multicolor Slack mark on a neutral tile. Below 22 px it
draws the bare glyph in the brand color. Any other channel gets a monogram
tile.
