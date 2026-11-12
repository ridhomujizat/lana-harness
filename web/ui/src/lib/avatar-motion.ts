/* Avatar motion (prototype design/prototypes/avatar-motion.html). A generated
   avatar is rigged by lib/avatar-svg: g.av-squash > body + g.av-look >
   g.av-gaze > g.av-eye. The mood of an avatar host is its data-mood attribute;
   styles/pages-gr8r/avatar-motion.css draws each mood in CSS alone.

   Only the head avatar of a working turn in the room chat moves (owner,
   2026-11-13: page-wide gaze, blinks and idle motion made the web heavy). */

export type AvatarMood =
  | "rest"
  | "idle"
  | "thinking"
  | "tool"
  | "talk"
  | "ask"
  | "error"
  | "done"
  | "sleep";
